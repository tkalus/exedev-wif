package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

var (
	errInvalidIntegration  = errors.New("invalid integration name")
	errGCPConfigPositional = errors.New("gcp-config takes no positional arguments")
	errGCPMetadataMissing  = errors.New("GCP metadata missing field")
	errGCPMetadataInvalid  = errors.New("GCP metadata has invalid field")
)

func runGCPSetup(args []string, stdout io.Writer) error {
	if wantsHelp(args) {
		writeGCPSetupHelp(stdout)

		return nil
	}

	flags := flag.NewFlagSet("gcp-setup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	integration := flags.String("integration", "default", "exe.dev integration name")
	team := flags.Bool("team", false, "use a team integration")

	err := flags.Parse(args)
	if err != nil {
		return fmt.Errorf("parse gcp-setup flags: %w", err)
	}

	if flags.NArg() != 0 {
		return fmt.Errorf("gcp-setup %w", errSetupPositional)
	}

	if !integrationRE.MatchString(*integration) {
		return errInvalidIntegration
	}

	executable, err := installedExecutable()
	if err != nil {
		return err
	}

	return writeGCPSetup(stdout, executable, *integration, *team)
}

func writeGCPSetupHelp(writer io.Writer) {
	_, _ = io.WriteString(writer, `usage: exedev-wif gcp-setup [--integration NAME] [--team]

Print a shell script that can be piped to bash:

  exedev-wif gcp-setup --integration gcpwif | bash

The script reads integration metadata and installs the URL-sourced external-account
configuration at ~/.config/gcloud/application_default_credentials.json (or under
CLOUDSDK_CONFIG). SDKs use it as Application Default Credentials and refresh tokens
automatically. If gcloud is installed, it also logs in using this configuration.
No GCP daemon, gcloud installation, or service-account key is needed.

Options:
  --integration NAME  exe.dev integration name. Default: default.
  --team              Use the team integration hostname.
`)
}

func runGCPConfig(args []string, stdout io.Writer) error {
	if wantsHelp(args) {
		_, _ = io.WriteString(stdout, `usage: exedev-wif gcp-config [--integration NAME] [--team]

Read GCP integration metadata and print a URL-sourced external-account JSON config.
The integration defaults to default. --team uses NAME.team.exe.xyz.
`)

		return nil
	}

	flags := flag.NewFlagSet("gcp-config", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	integration := flags.String("integration", "default", "exe.dev integration name")
	team := flags.Bool("team", false, "use a team integration")

	err := flags.Parse(args)
	if err != nil {
		return fmt.Errorf("parse gcp-config flags: %w", err)
	}

	if flags.NArg() != 0 {
		return errGCPConfigPositional
	}

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	return writeGCPConfig(ctx, http.DefaultClient, *integration, *team, stdout)
}

func writeGCPConfig(ctx context.Context, client *http.Client, integration string, team bool, stdout io.Writer) error {
	if !integrationRE.MatchString(integration) {
		return errInvalidIntegration
	}

	domain := "int"
	if team {
		domain = "team"
	}

	baseURL := "https://" + integration + "." + domain + ".exe.xyz"

	metadata, err := getJSON[gcpMetadata](ctx, client, baseURL+"/metadata")
	if err != nil {
		return fmt.Errorf("fetch GCP metadata: %w", err)
	}

	err = metadata.validate()
	if err != nil {
		return err
	}

	resource := fmt.Sprintf("projects/%s/locations/global/workloadIdentityPools/%s/providers/%s",
		metadata.ProjectNumber, metadata.PoolID, metadata.ProviderID)
	//nolint:gosec // This contains public federation endpoints and identifiers, not credentials.
	configuration := gcpCredentialConfig{
		Type:             "external_account",
		Audience:         "//iam.googleapis.com/" + resource,
		SubjectTokenType: "urn:ietf:params:oauth:token-type:jwt",
		TokenURL:         "https://sts.googleapis.com/v1/token",
		ServiceAccountImpersonationURL: "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/" +
			url.PathEscape(metadata.ServiceAccount) + ":generateAccessToken",
		CredentialSource: gcpCredentialSource{
			URL: baseURL + "/token",
			Format: gcpCredentialFormat{
				Type:                  "json",
				SubjectTokenFieldName: "token",
			},
		},
	}

	err = json.NewEncoder(stdout).Encode(configuration)
	if err != nil {
		return fmt.Errorf("write GCP credential configuration: %w", err)
	}

	return nil
}

//nolint:tagliatelle // Google and exe.dev use snake_case JSON field names.
type gcpMetadata struct {
	ProjectNumber  string `json:"project_number"`
	PoolID         string `json:"pool_id"`
	ProviderID     string `json:"provider_id"`
	ServiceAccount string `json:"service_account"`
}

func (m gcpMetadata) validate() error {
	for _, field := range []struct{ name, value string }{
		{"project_number", m.ProjectNumber},
		{"pool_id", m.PoolID},
		{"provider_id", m.ProviderID},
		{"service_account", m.ServiceAccount},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%w: %s", errGCPMetadataMissing, field.name)
		}

		if strings.ContainsAny(field.value, "/?# \t\r\n") {
			return fmt.Errorf("%w: %s", errGCPMetadataInvalid, field.name)
		}
	}

	return nil
}

//nolint:tagliatelle // Google external-account configuration uses snake_case JSON field names.
type gcpCredentialConfig struct {
	Type                           string              `json:"type"`
	Audience                       string              `json:"audience"`
	SubjectTokenType               string              `json:"subject_token_type"`
	TokenURL                       string              `json:"token_url"`
	ServiceAccountImpersonationURL string              `json:"service_account_impersonation_url"`
	CredentialSource               gcpCredentialSource `json:"credential_source"`
}

type gcpCredentialSource struct {
	URL    string              `json:"url"`
	Format gcpCredentialFormat `json:"format"`
}

type gcpCredentialFormat struct {
	Type string `json:"type"`
	//nolint:tagliatelle // Google external-account configuration uses snake_case JSON field names.
	SubjectTokenFieldName string `json:"subject_token_field_name"`
}

func writeGCPSetup(stdout io.Writer, executable, integration string, team bool) error {
	teamFlag := ""
	if team {
		teamFlag = "--team"
	}

	_, err := fmt.Fprintf(stdout, gcpSetupScript, shellQuote(executable), shellQuote(integration), shellQuote(teamFlag))
	if err != nil {
		return fmt.Errorf("write GCP setup script: %w", err)
	}

	return nil
}

const gcpSetupScript = `#!/usr/bin/env bash
set -euo pipefail
umask 077

exedev_wif=%s
integration=%s
team_flag=%s
gcloud_dir="${CLOUDSDK_CONFIG:-$HOME/.config/gcloud}"
credentials_file="$gcloud_dir/application_default_credentials.json"
mkdir -p "$gcloud_dir"
temporary_file="$(mktemp "$gcloud_dir/.exedev-wif.XXXXXXXX")"
trap 'rm -f "$temporary_file"' EXIT

args=(gcp-config --integration "$integration")
if [[ -n "$team_flag" ]]; then
  args+=("$team_flag")
fi
"$exedev_wif" "${args[@]}" >"$temporary_file"

if [[ -e "$credentials_file" ]]; then
  cp "$credentials_file" "$credentials_file.bak.$(date +%%Y%%m%%d%%H%%M%%S)"
fi
mv "$temporary_file" "$credentials_file"
printf 'Installed GCP Application Default Credentials at %%s\n' "$credentials_file"

if command -v gcloud >/dev/null 2>&1; then
  gcloud auth login --cred-file="$credentials_file" --quiet
fi
`
