package main

import (
	"context"
	"io"
	"net/http"
	"time"
)

func ExportTestErrUnknownCommand() error {
	return errUnknownCommand
}

func ExportTestErrServePositional() error {
	return errServePositional
}

func ExportTestErrGetUsage() error {
	return errGetUsage
}

func ExportTestErrSetupPositional() error {
	return errSetupPositional
}

func ExportTestErrUnexpectedStatus() error {
	return errUnexpectedStatus
}

type ExportTestCredentialProcessResponse = credentialProcessResponse

func ExportTestRunWithOutput(args []string, stdout io.Writer) error {
	return runWithOutput(args, stdout)
}

func ExportTestWantsHelp(args []string) bool {
	return wantsHelp(args)
}

func ExportTestShellQuote(value string) string {
	return shellQuote(value)
}

//nolint:ireturn // Test wrapper preserves the generic helper signature under test.
func ExportTestGetJSON[T any](ctx context.Context, client *http.Client, url string) (T, error) {
	return getJSON[T](ctx, client, url)
}

func ExportTestStoreCredentials(
	service *ExeDev,
	integration string,
	credentials *credentialProcessResponse,
) {
	service.storeCredentials(integration, credentials)
}

func ExportTestCachedCredentials(
	service *ExeDev,
	integration string,
) *credentialProcessResponse {
	return service.cachedCredentials(integration)
}

func ExportTestCredentialCacheSkew() time.Duration {
	return credentialCacheSkew
}
