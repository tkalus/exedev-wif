package main_test

//nolint:depguard,importas // External tests intentionally import the command package via export_test wrappers.
import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	exedev "tkalus.dev/exedev-wif"
)

const testAWSRegion = "us-east-1"

func TestGetJSONDecodesOKResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", request.Method)
		}

		_, _ = writer.Write([]byte(`{"name":"alice"}`))
	}))
	t.Cleanup(server.Close)

	got, err := exedev.ExportTestGetJSON[struct {
		Name string `json:"name"`
	}](context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("get JSON: %v", err)
	}

	if got.Name != "alice" {
		t.Fatalf("name = %q, want alice", got.Name)
	}
}

func TestGetJSONReturnsUnexpectedStatus(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "nope", http.StatusTeapot)
	}))
	t.Cleanup(server.Close)

	_, err := exedev.ExportTestGetJSON[struct{}](context.Background(), server.Client(), server.URL)
	if !errors.Is(err, exedev.ExportTestErrUnexpectedStatus()) {
		t.Fatalf("error = %v, want unexpected status", err)
	}
}

func TestGetJSONReturnsDecodeError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`not json`))
	}))
	t.Cleanup(server.Close)

	_, err := exedev.ExportTestGetJSON[struct{}](context.Background(), server.Client(), server.URL)
	if err == nil {
		t.Fatal("error = nil, want decode error")
	}

	assertContains(t, err.Error(), "decode response")
}

func TestGetJSONReturnsRequestError(t *testing.T) {
	t.Parallel()

	_, err := exedev.ExportTestGetJSON[struct{}](context.Background(), http.DefaultClient, "http://[::1")
	if err == nil {
		t.Fatal("error = nil, want request error")
	}

	assertContains(t, err.Error(), "create request")
}

func TestExeDevHandleRejectsUnsupportedProvider(t *testing.T) {
	t.Parallel()

	service := exedev.NewExeDev(testAWSConfig())
	response := handleRequest(service, "/gcp/default")

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestExeDevHandleRejectsInvalidIntegration(t *testing.T) {
	t.Parallel()

	service := exedev.NewExeDev(testAWSConfig())
	response := handleRequest(service, "/aws/Invalid")

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestExeDevHandleReturnsCachedCredentials(t *testing.T) {
	t.Parallel()

	expiration := time.Now().Add(time.Hour)
	service := exedev.NewExeDev(testAWSConfig())
	exedev.ExportTestStoreCredentials(service, "default", &exedev.ExportTestCredentialProcessResponse{
		Version:         1,
		AccessKeyID:     "access-key",
		SecretAccessKey: "secret-key",
		SessionToken:    "session-token",
		Expiration:      expiration,
	})

	response := handleRequest(service, "/aws/default")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}

	assertContains(t, response.Header().Get("Content-Type"), "application/json")

	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}

	assertContains(t, response.Body.String(), `"Version":1`)
	assertContains(t, response.Body.String(), `"AccessKeyId":"access-key"`)
	assertContains(t, response.Body.String(), `"SecretAccessKey":"secret-key"`)
	assertContains(t, response.Body.String(), `"SessionToken":"session-token"`)
}

func TestCredentialCacheReturnsUnexpiredCredentials(t *testing.T) {
	t.Parallel()

	service := exedev.NewExeDev(testAWSConfig())
	//nolint:exhaustruct // Cache tests only need expiration behavior.
	credentials := &exedev.ExportTestCredentialProcessResponse{
		Expiration: time.Now().Add(exedev.ExportTestCredentialCacheSkew() + time.Minute),
	}
	exedev.ExportTestStoreCredentials(service, "default", credentials)

	got := exedev.ExportTestCachedCredentials(service, "default")
	if got != credentials {
		t.Fatalf("cached credentials = %p, want %p", got, credentials)
	}
}

func TestCredentialCacheDropsExpiringCredentials(t *testing.T) {
	t.Parallel()

	service := exedev.NewExeDev(testAWSConfig())
	//nolint:exhaustruct // Cache tests only need expiration behavior.
	credentials := &exedev.ExportTestCredentialProcessResponse{
		Expiration: time.Now().Add(exedev.ExportTestCredentialCacheSkew() - time.Minute),
	}
	exedev.ExportTestStoreCredentials(service, "default", credentials)

	got := exedev.ExportTestCachedCredentials(service, "default")
	if got != nil {
		t.Fatalf("cached credentials = %p, want nil", got)
	}
}

func handleRequest(service *exedev.ExeDev, target string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{provider}/{integration}", service.Handle)

	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	return response
}

func testAWSConfig() aws.Config {
	//nolint:exhaustruct // Tests only need a region to construct ExeDev without default-region behavior.
	return aws.Config{Region: testAWSRegion}
}
