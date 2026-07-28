# exedev-wif

`exedev-wif` is a small AWS `credential_process` helper for exe.dev's
[AWS WIF](https://exe.dev/docs/integrations-aws-wif) integration

It runs a long-lived Unix-socket server that caches AWS STS credentials in
memory, and a short-lived `get` command that asks that server for credentials
and writes AWS credential-process JSON to stdout.

## Usage

Setup an AWS integration described in exe.dev's docs.

Install the user systemd service and write an AWS default profile config:

```sh
exedev-wif setup | bash
```

The setup command prints the shell script instead of running it directly. The
script creates `~/.config/systemd/user/exedev-wif.service`, enables and starts
that user service, and writes `~/.aws/config` so the `default` profile uses
`credential_process`.

By default, setup configures the credential route as `/aws/default`. Pass an
integration name when your exe.dev AWS WIF integration has a different name:

```sh
exedev-wif setup --integration awswif-green | bash
```

Inspect setup options with:

```sh
exedev-wif setup --help
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

## Route Format

The credential route is:

```text
/{provider}/{integration}
```

Only `aws` is currently accepted as the provider. The integration name must be
the exe.dev integration name, such as `awswif-green`.

## Development

Run checks with:

```sh
go test ./...
golangci-lint run ./...
```

## License

MIT. See [`LICENSE`](./LICENSE).
