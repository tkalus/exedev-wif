# exedev-wif

`exedev-wif` configures keyless AWS and GCP authentication using exe.dev's
[AWS WIF](https://exe.dev/docs/integrations-aws-wif) and
[GCP WIF](https://exe.dev/docs/integrations-gcp-wif) integrations.

It runs a long-lived Unix-socket server that caches AWS STS credentials in
memory, and a short-lived `get` command that asks that server for credentials
and writes AWS credential-process JSON to stdout.

## Usage

### AWS

Setup an AWS integration described in exe.dev's docs.

Install the user systemd service and write an AWS default profile config:

```sh
exedev-wif aws-setup | bash
```

The setup command prints the shell script instead of running it directly. The
script creates `~/.config/systemd/user/exedev-wif.service`, enables and starts
that user service, and writes `~/.aws/config` so the `default` profile uses
`credential_process`.

By default, setup configures the credential route as `/aws/default`. Pass an
integration name when your exe.dev AWS WIF integration has a different name:

```sh
exedev-wif aws-setup --integration awswif-green | bash
```

Inspect setup options with:

```sh
exedev-wif aws-setup --help
```

Run the foreground server manually:

```sh
exedev-wif serve --socket /run/user/1000/exedev-wif.sock
```

Fetch credentials for an exe.dev AWS WIF integration:

```sh
exedev-wif get --socket /run/user/1000/exedev-wif.sock /aws/awswif-green
```

Use it from AWS config:

```ini
[profile default]
credential_process = exedev-wif get --socket /run/user/1000/exedev-wif.sock /aws/awswif-green
```

Both commands default to the socket returned by
`httpunixagent.RuntimePaths("exedev-wif")`, so `--socket` is optional when the
server and client run in the same runtime environment.

General help is available with:

```sh
exedev-wif --help
exedev-wif serve --help
exedev-wif get --help
```

### GCP

First create and attach the integration and configure Google's pool, provider,
service account, and IAM binding using the
[exe.dev setup instructions](https://exe.dev/docs/integrations-gcp-wif).
Then run this on the attached VM:

```sh
exedev-wif gcp-setup --integration gcpwif-blue | bash
```

Setup reads `project_number`, `pool_id`, `provider_id`, and `service_account`
from the integration's `/metadata` endpoint and generates Google's
`external_account` credential configuration. The default integration name is
`default`; add `--team` for a team integration.

The script installs the configuration with owner-only permissions at
`~/.config/gcloud/application_default_credentials.json`, backing up an existing
file before replacing it. Google SDKs discover this standard Application Default
Credentials (ADC) file in new sessions without setting environment variables.
The SDK fetches fresh OIDC tokens from the integration's `/token` endpoint,
exchanges them through Google STS, and impersonates the configured service account.
There is no local token file, GCP daemon, service-account key, or `gcloud` dependency.

If `gcloud` is installed, setup also runs `gcloud auth login --cred-file=...`
to register and select the service account in its persistent CLI configuration.
In a new session, both of these work without exporting credentials:

```sh
gcloud auth print-access-token >/dev/null
gcloud auth application-default print-access-token >/dev/null
```

If you install `gcloud` later, rerun setup to register its CLI credentials.
Authentication and default project selection are separate; set a project once
if your commands need one: `gcloud config set project YOUR_PROJECT_ID`.
IAM grants on the service account determine access; setup doesn't grant roles.

`CLOUDSDK_CONFIG`, when explicitly set, selects a different installation directory;
keep it consistent for setup and consumers. Existing `GOOGLE_APPLICATION_CREDENTIALS`
or CLI credential overrides take precedence over this default setup. Other users
and containers need their own configuration or a mounted credential file.

To generate a configuration without installing it:

```sh
exedev-wif gcp-config --integration gcpwif-blue > credentials.json
```

### Azure (planned)

Azure support will let applications use their normal Azure SDK credentials when
launched through `exedev-wif`. The launcher will supply credentials for one
configured Entra application/service principal through a temporary local
managed-identity-compatible endpoint.

This section describes the proposed consumer interface. Azure commands and
configuration are not implemented yet. The exe.dev-to-Entra federation flow and
SDK compatibility still need live validation.

#### One-time setup

The cloud-side prerequisites are:

1. Create an exe.dev WIF integration with audience `api://AzureADTokenExchange`,
   attached to the workload's VM or tag. Use its exact generated issuer and subject.
   Confirm the generic WIF integration accepts this audience; exe.dev currently
   documents dedicated AWS and GCP recipes, not an Azure recipe.
2. Create an Entra application and its service principal.
3. Add a federated identity credential matching that issuer, subject, and audience.
4. Assign the identity the resource-specific Azure RBAC roles it needs.

For an application, create `federated-credential.json` with these values:

```json
{
  "name": "exe-dev",
  "issuer": "https://exe.dev/issuer/EXACT_GENERATED_ISSUER_ID",
  "subject": "EXACT_GENERATED_SUBJECT",
  "audiences": ["api://AzureADTokenExchange"]
}
```

Register it using an account authorized to manage the application's credentials:

```sh
az ad app federated-credential create \
  --id APPLICATION_CLIENT_ID --parameters federated-credential.json
```

The proposed local setup command will save the integration's tenant, client ID,
and allowed token resources in owner-only configuration and start the Unix-socket
agent. These are identifiers and policy settings. No client secret or certificate
is required.

```sh
# Planned command, not available yet.
exedev-wif azure-setup --integration azurewif-yellow \
  --tenant-id TENANT_ID --client-id APPLICATION_CLIENT_ID \
  --allow-resource https://vault.azure.net \
  --allow-resource https://storage.azure.com/ | bash
```

The setup script will configure the agent rather than edit shell startup files.
It will not grant Azure permissions. Resource access remains controlled by the
service principal's Azure roles and the target service's authorization rules.

#### Run an application

In any new session, launch the application through `exedev-wif`:

```sh
# Planned commands, not available yet.
exedev-wif azure-exec azurewif-yellow -- python app.py
exedev-wif azure-exec azurewif-yellow -- ./my-go-app
```

The integration name is a required positional argument. `--` separates it from
the child command, whose arguments are passed through unchanged. Any launcher
options, such as `--socket PATH`, go before the integration name:

```sh
# Planned command, not available yet.
exedev-wif azure-exec --socket /run/user/1000/exedev-wif.sock azurewif-yellow -- python app.py
```

There are no environment variables to export manually, and the Azure CLI is not
required. Python applications can keep using their usual SDK credential:

```python
from azure.identity import DefaultAzureCredential
from azure.keyvault.secrets import SecretClient

client = SecretClient(
    vault_url="https://my-vault.vault.azure.net",
    credential=DefaultAzureCredential(),
)
secret = client.get_secret("my-secret")
```

The application must use an Azure Identity version that supports selecting
`ManagedIdentityCredential` through `AZURE_TOKEN_CREDENTIALS`. Tested minimum
versions will be published with the implementation. Applications that select a
different credential explicitly will need to use `ManagedIdentityCredential`
or a compatible `DefaultAzureCredential` instead.

The same launch command can be used as a service's executable. Child processes
inherit the credential endpoint, so a supervised command can start workers that
use the same identity. Containers with a separate network namespace need explicit
support because their loopback interface cannot reach the launcher's listener.

#### What happens during a launch

The launcher binds an HTTP listener to `127.0.0.1` on an ephemeral port and creates
a fresh random authentication secret. It passes these settings only to the child
process and its descendants:

```text
IDENTITY_ENDPOINT=http://127.0.0.1:<ephemeral-port>/token
IDENTITY_HEADER=<per-launch secret>
AZURE_TOKEN_CREDENTIALS=ManagedIdentityCredential
```

It will replace conflicting identity-discovery settings in the child's environment
and bind any client-ID selection to the configured application. Selecting only
`ManagedIdentityCredential` prevents a compatible default credential chain from
silently using an existing CLI login or another identity if the bridge fails.
The parent shell's environment is unchanged.

When the SDK needs a token, the flow is:

```text
Azure SDK requests a token for a resource
  -> authenticated loopback HTTP bridge in the launcher
  -> exedev-wif agent over its protected Unix socket
  -> exe.dev OIDC assertion exchanged with Microsoft Entra
  -> resource-specific access token and expiry returned to the SDK
```

The endpoint speaks the App Service managed-identity protocol. The resulting
token represents the configured federated application/service principal. SDK
requests trigger renewal as needed, and the agent caches access tokens in memory
by identity and resource. Applications don't manage an OIDC token file or schedule
periodic logins.

Each launch has its own endpoint and secret. The launcher stays alive to supervise
the command, forwards termination signals, and returns the command's exit status.
When the command exits, the endpoint closes and stops issuing tokens, including
to surviving descendants. Tokens already obtained by an application remain valid
until their expiry.

#### Access and failure behavior

Every launch selects one configured integration. Requests cannot switch its
tenant, application, or token issuer. Both the bridge and agent will enforce the
configured resource allowlist. A token request for an unlisted resource fails
even if the service principal has permissions for it.

Token resources identify API audiences, such as Key Vault or Storage. They do
not identify an individual vault or storage account. Azure authorization controls
which resources the application can access within that audience.

The bridge will require its per-launch secret on every request and accept only
the supported token protocol. It will not forward arbitrary HTTP requests.
Authentication failures, unavailable agents, and denied resources will return
errors to the SDK. Access tokens, assertions, and launch secrets will not be
written to logs or persistent credential files. Supported SDK transports must
keep loopback credential traffic out of configured HTTP proxies.

The launched process and its descendants are trusted with this identity. The
launcher is not a sandbox. Other processes running as the same Unix user may be
able to read its environment or access the agent socket. Mutually untrusted
workloads need separate OS identities and restricted agent access, or stronger
container/VM isolation.

#### Azure CLI

The launcher is intended for Azure SDK applications. It will not log in ordinary
`az` commands or replace the Azure CLI executable. For a one-off CLI federation
check after configuring trust, use the existing CLI login flow:

```sh
az login --service-principal --username APPLICATION_CLIENT_ID \
  --tenant TENANT_ID \
  --federated-token "$(curl -fsS https://azurewif.int.exe.xyz/token | jq -r .token)"
```

This requires `curl`, `jq`, and `az`. The CLI caches the login, but a one-time
federated login does not provide indefinite renewal from fresh exe.dev assertions.

References: [Entra external-issuer trust](https://learn.microsoft.com/en-us/entra/workload-id/workload-identity-federation-create-trust),
[Azure SDK default credential chain](https://learn.microsoft.com/en-us/azure/developer/go/sdk/authentication/credential-chains),
and [App Service token endpoint protocol](https://learn.microsoft.com/en-us/azure/app-service/overview-managed-identity#rest-endpoint-reference).

## Route Format

The credential route is:

```text
/{provider}/{integration}
```

The agent route accepts `aws`. GCP uses the integration directly through the
generated credential configuration. The integration name must be
the exe.dev integration name, such as `awswif-green`.

## Development

Run checks with:

```sh
go test ./...
golangci-lint run ./...
```

## License

MIT. See [`LICENSE`](./LICENSE).
