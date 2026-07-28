package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

const (
	defaultAWSRegion     = "us-east-1"
	exeDevDefaultTimeout = 15 * time.Second
	credentialCacheSkew  = 5 * time.Minute
	hostnamePath         = "/etc/hostname"
)

var integrationRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

var (
	errMetadataRoleARNMissing = errors.New("metadata missing role_arn")
	errTokenMissing           = errors.New("token missing")
	errNoSTSCredentials       = errors.New("STS returned no credentials")
	errUnexpectedStatus       = errors.New("unexpected status")
	errUnsupportedProvider    = errors.New("unsupported provider")
)

type ExeDev struct {
	logger     *slog.Logger
	httpClient *http.Client
	stsClient  *sts.Client
	timeout    time.Duration
	cacheMu    sync.Mutex
	cache      map[string]*credentialProcessResponse
}

type ExeDevOption func(*ExeDev)

func WithExeDevLogger(logger *slog.Logger) ExeDevOption {
	return func(service *ExeDev) {
		service.logger = logger
	}
}

func WithExeDevHTTPClient(client *http.Client) ExeDevOption {
	return func(service *ExeDev) {
		service.httpClient = client
	}
}

func WithExeDevTimeout(timeout time.Duration) ExeDevOption {
	return func(service *ExeDev) {
		service.timeout = timeout
	}
}

func NewExeDev(
	cfg aws.Config,
	opts ...ExeDevOption,
) *ExeDev {
	if cfg.Region == "" {
		cfg.Region = defaultAWSRegion
	}

	service := &ExeDev{
		logger:     slog.Default(),
		httpClient: http.DefaultClient,
		stsClient:  sts.NewFromConfig(cfg),
		timeout:    exeDevDefaultTimeout,
		cacheMu:    sync.Mutex{},
		cache:      make(map[string]*credentialProcessResponse),
	}

	for _, opt := range opts {
		opt(service)
	}

	return service
}

func (e *ExeDev) Handle(writer http.ResponseWriter, request *http.Request) {
	provider := request.PathValue("provider")
	if provider != "aws" {
		e.logger.ErrorContext(
			request.Context(),
			"unsupported provider",
			"error",
			errUnsupportedProvider,
			"provider",
			provider,
		)
		http.Error(writer, "unsupported provider", http.StatusBadRequest)

		return
	}

	integration := request.PathValue("integration")
	if !integrationRE.MatchString(integration) {
		http.Error(writer, "invalid integration", http.StatusBadRequest)

		return
	}

	ctx, cancel := context.WithTimeout(request.Context(), e.timeout)
	defer cancel()

	credentials, err := e.getCredentials(ctx, integration)
	if err != nil {
		e.writeError(ctx, writer, "get credentials", err)

		return
	}

	e.writeCredentials(ctx, writer, credentials)
}

func (e *ExeDev) getCredentials(
	ctx context.Context,
	integration string,
) (*credentialProcessResponse, error) {
	if cached := e.cachedCredentials(integration); cached != nil {
		return cached, nil
	}

	metadata, err := e.getMetadata(ctx, integration)
	if err != nil {
		return nil, fmt.Errorf("fetch metadata: %w", err)
	}

	if metadata.RoleARN == "" {
		return nil, errMetadataRoleARNMissing
	}

	token, err := e.getToken(ctx, integration)
	if err != nil {
		return nil, fmt.Errorf("fetch token: %w", err)
	}

	if token.Token == "" {
		return nil, errTokenMissing
	}

	response, err := e.stsClient.AssumeRoleWithWebIdentity(
		ctx,
		//nolint:exhaustruct // Optional AWS request fields intentionally use SDK zero values.
		&sts.AssumeRoleWithWebIdentityInput{
			RoleArn:          aws.String(metadata.RoleARN),
			RoleSessionName:  aws.String(roleSessionName(integration)),
			WebIdentityToken: aws.String(token.Token),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("AssumeRoleWithWebIdentity: %w", err)
	}

	if response.Credentials == nil {
		return nil, errNoSTSCredentials
	}

	credentials := &credentialProcessResponse{
		Version:         1,
		AccessKeyID:     aws.ToString(response.Credentials.AccessKeyId),
		SecretAccessKey: aws.ToString(response.Credentials.SecretAccessKey),
		SessionToken:    aws.ToString(response.Credentials.SessionToken),
		Expiration:      aws.ToTime(response.Credentials.Expiration),
	}

	e.storeCredentials(integration, credentials)

	return credentials, nil
}

func roleSessionName(integration string) string {
	return roleSessionNameFromHostname(hostnamePath, integration)
}

func roleSessionNameFromHostname(path string, integration string) string {
	hostnamePathname, err := os.ReadFile(path)
	if err != nil {
		return integration
	}

	hostame := strings.TrimSpace(string(hostnamePathname))
	if hostame == "" {
		return integration
	}

	return hostame
}

func (e *ExeDev) cachedCredentials(integration string) *credentialProcessResponse {
	e.cacheMu.Lock()
	defer e.cacheMu.Unlock()

	credentials := e.cache[integration]
	if credentials == nil {
		return nil
	}

	if time.Until(credentials.Expiration) <= credentialCacheSkew {
		delete(e.cache, integration)

		return nil
	}

	return credentials
}

func (e *ExeDev) storeCredentials(integration string, credentials *credentialProcessResponse) {
	e.cacheMu.Lock()
	defer e.cacheMu.Unlock()

	e.cache[integration] = credentials
}

func (e *ExeDev) writeCredentials(
	ctx context.Context,
	writer http.ResponseWriter,
	credentials *credentialProcessResponse,
) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")

	//nolint:gosec // This endpoint intentionally emits AWS credential_process JSON to stdout consumers.
	err := json.NewEncoder(writer).Encode(credentials)
	if err != nil {
		e.logger.ErrorContext(
			ctx,
			"encode credential response",
			"error",
			err,
		)
	}
}

func (e *ExeDev) endpoint(
	integration string,
	path string,
) string {
	return fmt.Sprintf(
		"https://%s.int.exe.xyz/%s",
		integration,
		path,
	)
}

func (e *ExeDev) getMetadata(
	ctx context.Context,
	integration string,
) (*exeDevMetadata, error) {
	metadata, err := getJSON[exeDevMetadata](
		ctx,
		e.httpClient,
		e.endpoint(integration, "metadata"),
	)
	if err != nil {
		return nil, err
	}

	return &metadata, nil
}

func (e *ExeDev) getToken(
	ctx context.Context,
	integration string,
) (*exeDevToken, error) {
	token, err := getJSON[exeDevToken](
		ctx,
		e.httpClient,
		e.endpoint(integration, "token"),
	)
	if err != nil {
		return nil, err
	}

	return &token, nil
}

//nolint:ireturn // Generic JSON decoding returns the caller-selected concrete response type.
func getJSON[T any](
	ctx context.Context,
	client *http.Client,
	url string,
) (T, error) {
	var value T

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		url,
		nil,
	)
	if err != nil {
		return value, fmt.Errorf("create request: %w", err)
	}

	response, err := client.Do(request)
	if err != nil {
		return value, fmt.Errorf("perform request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return value, fmt.Errorf("%w: %s", errUnexpectedStatus, response.Status)
	}

	err = json.NewDecoder(response.Body).Decode(&value)
	if err != nil {
		return value, fmt.Errorf("decode response: %w", err)
	}

	return value, nil
}

func (e *ExeDev) writeError(
	ctx context.Context,
	writer http.ResponseWriter,
	message string,
	err error,
) {
	e.logger.ErrorContext(
		ctx,
		message,
		"error",
		err,
	)

	http.Error(
		writer,
		message,
		http.StatusBadGateway,
	)
}

type exeDevMetadata struct {
	//nolint:tagliatelle // exe.dev metadata uses role_arn.
	RoleARN string `json:"role_arn"`
}

type exeDevToken struct {
	Token string `json:"token"`
}

type credentialProcessResponse struct {
	//nolint:tagliatelle // AWS credential_process requires capitalized JSON field names.
	Version int `json:"Version"`
	//nolint:tagliatelle // AWS credential_process requires capitalized JSON field names.
	AccessKeyID string `json:"AccessKeyId"`
	//nolint:tagliatelle // AWS credential_process requires capitalized JSON field names.
	SecretAccessKey string `json:"SecretAccessKey"`
	//nolint:tagliatelle // AWS credential_process requires capitalized JSON field names.
	SessionToken string `json:"SessionToken"`
	//nolint:tagliatelle // AWS credential_process requires capitalized JSON field names.
	Expiration time.Time `json:"Expiration"`
}
