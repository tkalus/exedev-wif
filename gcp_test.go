package main_test

//nolint:depguard,importas // External tests use command-package test exports.
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	exedev "tkalus.dev/exedev-wif"
)

const gcpTestMetadata = `{"project_number":"123456789012","pool_id":"exe-pool",` +
	`"provider_id":"exe-provider","service_account":"workload@example.iam.gserviceaccount.com"}`

type metadataTransport struct {
	body string
	host string
}

func (m metadataTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host != m.host || request.URL.Path != "/metadata" {
		return nil, fmt.Errorf("unexpected metadata request: %w", exedev.ExportTestErrUnexpectedStatus())
	}

	//nolint:exhaustruct,exhaustruct_v5 // Only fields consumed by the metadata reader are needed.
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(m.body))}, nil
}

func TestGCPConfigUsesIntegrationMetadata(t *testing.T) {
	t.Parallel()

	for _, team := range []bool{false, true} {
		domain := "int"
		if team {
			domain = "team"
		}

		host := "gcpwif." + domain + ".exe.xyz"
		//nolint:exhaustruct,exhaustruct_v5 // Only a fake transport is required.
		client := &http.Client{Transport: metadataTransport{body: gcpTestMetadata, host: host}}

		var output bytes.Buffer

		err := exedev.ExportTestWriteGCPConfig(context.Background(), client, "gcpwif", team, &output)
		if err != nil {
			t.Fatalf("generate config: %v", err)
		}

		var config map[string]any

		err = json.Unmarshal(output.Bytes(), &config)
		if err != nil {
			t.Fatalf("decode config: %v", err)
		}

		//nolint:gosec // Expected public federation endpoints contain no secrets.
		want := map[string]any{
			"type": "external_account",
			"audience": "//iam.googleapis.com/projects/123456789012/locations/global/" +
				"workloadIdentityPools/exe-pool/providers/exe-provider",
			"subject_token_type": "urn:ietf:params:oauth:token-type:jwt",
			"token_url":          "https://sts.googleapis.com/v1/token",
			"service_account_impersonation_url": "https://iamcredentials.googleapis.com/v1/projects/-/" +
				"serviceAccounts/workload@example.iam.gserviceaccount.com:generateAccessToken",
		}
		for key, value := range want {
			if config[key] != value {
				t.Fatalf("%s = %v, want %v", key, config[key], value)
			}
		}

		assertContains(t, output.String(), `"url":"https://`+host+`/token"`)
		assertContains(t, output.String(), `"format":{"type":"json","subject_token_field_name":"token"}`)
	}
}

func TestGCPConfigRejectsIncompleteMetadata(t *testing.T) {
	t.Parallel()

	//nolint:exhaustruct,exhaustruct_v5 // Only a fake transport is required.
	client := &http.Client{Transport: metadataTransport{body: `{}`, host: "default.int.exe.xyz"}}

	var output bytes.Buffer

	err := exedev.ExportTestWriteGCPConfig(context.Background(), client, "default", false, &output)
	if err == nil || output.Len() != 0 {
		t.Fatalf("error = %v, output = %q; want error and no config", err, output.String())
	}
}

func TestGCPSetupInstallsDefaultCredentials(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	helper := filepath.Join(dir, "exedev wif")
	//nolint:gosec // This is an executable test helper in a private temporary directory.
	err := os.WriteFile(helper, []byte("#!/bin/sh\n"+
		"test \"$1\" = gcp-config && test \"$2\" = --integration && test \"$3\" = gcpwif || exit 1\n"+
		"printf '%s\\n' '{\"type\":\"external_account\"}'\n"), 0o700)
	if err != nil {
		t.Fatalf("write helper: %v", err)
	}

	var script bytes.Buffer

	err = exedev.ExportTestWriteGCPSetup(&script, helper, "gcpwif", false)
	if err != nil {
		t.Fatalf("render setup: %v", err)
	}

	command := exec.CommandContext(t.Context(), "bash")
	command.Stdin = bytes.NewReader(script.Bytes())
	command.Env = []string{"HOME=" + dir, "PATH=/usr/bin:/bin"}

	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("execute setup: %v: %s", err, output)
	}

	credentials := filepath.Join(dir, ".config", "gcloud", "application_default_credentials.json")

	contents, err := os.ReadFile(credentials)
	if err != nil {
		t.Fatalf("read default credentials: %v", err)
	}

	assertContains(t, string(contents), `"type":"external_account"`)

	info, err := os.Stat(credentials)
	if err != nil {
		t.Fatalf("stat default credentials: %v", err)
	}

	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials permissions = %o, want 600", info.Mode().Perm())
	}

	verifyGCPSetupLifecycle(t, dir, helper, credentials, script.Bytes(), contents)
}

func verifyGCPSetupLifecycle(t *testing.T, dir, helper, credentials string, script, contents []byte) {
	t.Helper()

	//nolint:gosec // This executable records CLI registration in a private test directory.
	err := os.WriteFile(filepath.Join(dir, "gcloud"), []byte(`#!/bin/sh
test "$1" = auth && test "$2" = login && test "$4" = --quiet || exit 1
test "$3" = "--cred-file=$HOME/.config/gcloud/application_default_credentials.json" || exit 1
test -f "$HOME/.config/gcloud/application_default_credentials.json" || exit 1
printf '%s\n' "$3" >"$HOME/gcloud-registration"
`), 0o700)
	if err != nil {
		t.Fatalf("write fake gcloud: %v", err)
	}

	command := exec.CommandContext(t.Context(), "bash")
	command.Stdin = bytes.NewReader(script)
	command.Env = []string{"HOME=" + dir, "PATH=" + dir + ":/usr/bin:/bin"}

	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("execute setup with gcloud: %v: %s", err, output)
	}

	_, err = os.Stat(filepath.Join(dir, "gcloud-registration"))
	if err != nil {
		t.Fatalf("CLI credentials were not registered: %v", err)
	}

	backups, err := filepath.Glob(credentials + ".bak.*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %v, error = %v; want one backup", backups, err)
	}

	//nolint:gosec // Replacing the executable test helper simulates a failed metadata fetch.
	err = os.WriteFile(helper, []byte("#!/bin/sh\nexit 1\n"), 0o700)
	if err != nil {
		t.Fatalf("write failing helper: %v", err)
	}

	command = exec.CommandContext(t.Context(), "bash")
	command.Stdin = bytes.NewReader(script)
	command.Env = []string{"HOME=" + dir, "PATH=/usr/bin:/bin"}

	output, err = command.CombinedOutput()
	if err == nil {
		t.Fatalf("setup succeeded despite failed metadata fetch: %s", output)
	}

	afterFailure, err := os.ReadFile(credentials)
	if err != nil || !bytes.Equal(contents, afterFailure) {
		t.Fatalf("failed setup modified default credentials: %q, error: %v", afterFailure, err)
	}
}
