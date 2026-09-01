// Package mupag fornece um SDK Go pequeno para consumir a API publica da MuPag.
package mupag

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	mathrand "math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL                   = "https://api.mupag.com.br"
	testBaseURL                      = "https://api.sandbox.mupag.com.br"
	defaultMaxResponseBytes    int64 = 2 * 1024 * 1024
	maxConfiguredResponseBytes int64 = 16 * 1024 * 1024
	maxRequestBytes                  = 1024 * 1024
)

type Environment string

const (
	EnvironmentTest Environment = "test"
	EnvironmentPrd  Environment = "prd"
)

// Client concentra os recursos publicos da API sem guardar estado mutavel global.
type Client struct {
	apiKey           string
	environment      Environment
	baseURL          string
	httpClient       *http.Client
	retryPolicy      RetryPolicy
	maxResponseBytes int64
	configErr        error

	Charges       *ChargesService
	Refunds       *RefundsService
	Subscriptions *SubscriptionsService
}

// RetryPolicy controla repeticoes curtas para falhas transientes 5xx.
type RetryPolicy struct {
	MaxRetries int
	BaseDelay  time.Duration
}

// Option configura o client sem expor campos internos.
type Option func(*Client)

// NewClient cria um client pronto para uso com net/http e timeout seguro.
func NewClient(opts ...Option) *Client {
	client := &Client{
		httpClient:       &http.Client{Timeout: 10 * time.Second},
		retryPolicy:      RetryPolicy{MaxRetries: 2, BaseDelay: 200 * time.Millisecond},
		maxResponseBytes: defaultMaxResponseBytes,
	}
	for _, opt := range opts {
		opt(client)
	}
	client.baseURL = strings.TrimRight(client.baseURL, "/")
	client.configErr = client.validateConfiguration()
	client.Charges = &ChargesService{client: client}
	client.Refunds = &RefundsService{client: client}
	client.Subscriptions = &SubscriptionsService{client: client}
	return client
}

// WithAPIKey define a chave enviada no header Authorization.
func WithAPIKey(apiKey string) Option {
	return func(client *Client) {
		client.apiKey = apiKey
	}
}

// WithBaseURL aponta o SDK para sandbox local, httptest ou ambiente dedicado.
func WithBaseURL(baseURL string) Option {
	return func(client *Client) {
		client.baseURL = baseURL
	}
}

// WithTestEnvironment usa o endpoint sandbox oficial da MuPag.
func WithTestEnvironment() Option {
	return func(client *Client) {
		client.environment = EnvironmentTest
		client.baseURL = testBaseURL
	}
}

// WithPrdEnvironment seleciona explicitamente o ambiente prd.
func WithPrdEnvironment() Option {
	return func(client *Client) {
		client.environment = EnvironmentPrd
		client.baseURL = defaultBaseURL
	}
}

// WithHTTPClient troca o client HTTP mantendo a serializacao e erros do SDK.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(client *Client) {
		if httpClient != nil {
			client.httpClient = httpClient
		}
	}
}

// WithRetryPolicy configura retries limitados para falhas transientes.
func WithRetryPolicy(policy RetryPolicy) Option {
	return func(client *Client) {
		client.retryPolicy = policy
	}
}

// WithMaxResponseBytes limita o corpo lido antes do parse JSON.
func WithMaxResponseBytes(maxBytes int64) Option {
	return func(client *Client) {
		client.maxResponseBytes = maxBytes
	}
}

type requestOptions struct {
	idempotencyKey    string
	idempotencyKeySet bool
}

// RequestOption ajusta uma chamada sem mudar o client compartilhado.
type RequestOption func(*requestOptions)

// WithIdempotencyKey preserva a chave idempotente definida pelo consumidor.
func WithIdempotencyKey(key string) RequestOption {
	return func(options *requestOptions) {
		options.idempotencyKey = key
		options.idempotencyKeySet = true
	}
}

func (client *Client) do(ctx context.Context, method, path string, query url.Values, body any, out any, opts ...RequestOption) error {
	if client.configErr != nil {
		return client.configErr
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestOptions := requestOptions{}
	for _, opt := range opts {
		opt(&requestOptions)
	}
	var encodedBody []byte
	var err error
	if body != nil {
		encodedBody, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("mupag: request JSON encoding failed: %w", err)
		}
		if len(encodedBody) > maxRequestBytes {
			return errors.New("mupag: request body exceeds 1 MiB")
		}
	}
	if isMutation(method) {
		if requestOptions.idempotencyKeySet {
			if err := validateIdempotencyKey(requestOptions.idempotencyKey); err != nil {
				return err
			}
		} else {
			requestOptions.idempotencyKey, err = randomHex(16)
			if err != nil {
				return err
			}
			requestOptions.idempotencyKey = "sdk-go-" + requestOptions.idempotencyKey
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	attempts := client.retryPolicy.MaxRetries + 1
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	var ambiguousErr error
	for attempt := 0; attempt < attempts; attempt++ {
		err = client.doOnce(ctx, method, path, query, encodedBody, out, requestOptions, ambiguousErr != nil)
		if err == nil {
			return nil
		}
		lastErr = err
		if isMutation(method) && ambiguousMutationError(err) && ambiguousErr == nil {
			ambiguousErr = err
		}
		if isMutation(method) && idempotencyOutcomeUnknown(err) {
			return &OutcomeUnknownError{IdempotencyKey: requestOptions.idempotencyKey, cause: err}
		}
		if attempt == attempts-1 || !retryableError(err) {
			if ambiguousErr != nil {
				return &OutcomeUnknownError{IdempotencyKey: requestOptions.idempotencyKey, cause: ambiguousErr}
			}
			return mutationOutcomeError(method, requestOptions.idempotencyKey, err)
		}
		if retryErr := waitForRetry(ctx, retryDelay(client.retryPolicy, attempt, err)); retryErr != nil {
			outcomeCause := lastErr
			if ambiguousErr != nil {
				outcomeCause = ambiguousErr
			}
			outcomeErr := mutationOutcomeError(method, requestOptions.idempotencyKey, outcomeCause)
			if _, unknown := outcomeErr.(*OutcomeUnknownError); unknown {
				return outcomeErr
			}
			return retryErr
		}
	}
	return lastErr
}

func (client *Client) doOnce(ctx context.Context, method, path string, query url.Values, body []byte, out any, opts requestOptions, afterAmbiguousFailure bool) error {
	endpoint, err := url.Parse(client.baseURL + path)
	if err != nil {
		return err
	}
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if client.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+client.apiKey)
	}
	if isMutation(method) {
		request.Header.Set("Idempotency-Key", opts.idempotencyKey)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return &transportError{cause: err}
	}
	defer response.Body.Close()
	responseBody, err := readBoundedBody(response, client.maxResponseBytes)
	if err != nil {
		if response.StatusCode == http.StatusConflict {
			return &unreadableConflictError{cause: err}
		}
		if response.StatusCode == http.StatusRequestTimeout ||
			response.StatusCode == http.StatusTooEarly ||
			response.StatusCode == http.StatusTooManyRequests ||
			response.StatusCode >= http.StatusInternalServerError {
			return decodeAPIError(response.StatusCode, response.Header, nil)
		}
		if response.StatusCode < http.StatusBadRequest {
			return &ambiguousResponseError{cause: err}
		}
		return decodeAPIError(response.StatusCode, response.Header, nil)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		responseErr := decodeAPIError(response.StatusCode, response.Header, responseBody)
		if response.StatusCode == http.StatusConflict {
			var apiErr *APIError
			if !AsAPIError(responseErr, &apiErr) {
				return &unreadableConflictError{cause: responseErr}
			}
			canonicalCode := strings.TrimSpace(apiErr.Code)
			if canonicalCode == "" || canonicalCode != apiErr.Code || canonicalCode == "http_409" {
				return &unreadableConflictError{cause: responseErr}
			}
		}
		return responseErr
	}
	if out == nil {
		return nil
	}
	if len(responseBody) == 0 {
		return &ambiguousResponseError{cause: errors.New("mupag: successful response body is empty")}
	}
	if err := json.Unmarshal(responseBody, out); err != nil {
		return &ambiguousResponseError{cause: errors.New("mupag: response is not valid JSON")}
	}
	if validator, ok := out.(responseValidator); ok {
		if err := validator.validateResponse(); err != nil {
			return &ambiguousResponseError{cause: err}
		}
	}
	if validator, ok := out.(ambiguousRetryResponseValidator); ok && afterAmbiguousFailure {
		if err := validator.validateResponseAfterAmbiguousRetry(); err != nil {
			return &ambiguousResponseError{cause: err}
		}
	}
	return nil
}

type responseValidator interface {
	validateResponse() error
}

type ambiguousRetryResponseValidator interface {
	validateResponseAfterAmbiguousRetry() error
}

func randomHex(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", errors.New("mupag: secure random generator unavailable")
	}
	return hex.EncodeToString(buffer), nil
}

type transportError struct{ cause error }

func (err *transportError) Error() string { return "mupag: network request failed" }
func (err *transportError) Unwrap() error { return err.cause }

type ambiguousResponseError struct{ cause error }

func (err *ambiguousResponseError) Error() string { return "mupag: response could not be confirmed" }
func (err *ambiguousResponseError) Unwrap() error { return err.cause }

type unreadableConflictError struct{ cause error }

func (err *unreadableConflictError) Error() string {
	return "mupag: conflict response could not be classified"
}
func (err *unreadableConflictError) Unwrap() error { return err.cause }

func (client *Client) validateConfiguration() error {
	if client.environment != EnvironmentTest && client.environment != EnvironmentPrd {
		return errors.New("mupag: environment must be selected explicitly")
	}
	if len(client.apiKey) < 9 || len(client.apiKey) > 512 || !visibleASCII(client.apiKey, false) {
		return errors.New("mupag: invalid API key")
	}
	expectedPrefix := "sk_test_"
	if client.environment == EnvironmentPrd {
		expectedPrefix = "sk_prd_"
	}
	if !strings.HasPrefix(client.apiKey, expectedPrefix) {
		return errors.New("mupag: API key does not match selected environment")
	}
	if err := validateBaseURL(client.baseURL, client.environment); err != nil {
		return err
	}
	if client.httpClient == nil {
		return errors.New("mupag: HTTP client is required")
	}
	httpClient := *client.httpClient
	if httpClient.Timeout == 0 {
		httpClient.Timeout = 10 * time.Second
	}
	if httpClient.Timeout < 0 || httpClient.Timeout > 120*time.Second {
		return errors.New("mupag: HTTP timeout must be between 1ns and 120s")
	}
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	client.httpClient = &httpClient
	if client.retryPolicy.MaxRetries < 0 || client.retryPolicy.MaxRetries > 5 ||
		client.retryPolicy.BaseDelay < 0 || client.retryPolicy.BaseDelay > 30*time.Second {
		return errors.New("mupag: invalid retry configuration")
	}
	if client.maxResponseBytes < 1 || client.maxResponseBytes > maxConfiguredResponseBytes {
		return errors.New("mupag: response limit must be between 1 byte and 16 MiB")
	}
	return nil
}

func validateBaseURL(raw string, environment Environment) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("mupag: invalid base URL")
	}
	canonicalRaw := testBaseURL
	if environment == EnvironmentPrd {
		canonicalRaw = defaultBaseURL
	}
	canonical, err := url.Parse(canonicalRaw)
	if err != nil {
		return errors.New("mupag: invalid canonical base URL")
	}
	canonicalOrigin := strings.EqualFold(parsed.Scheme, canonical.Scheme) && strings.EqualFold(parsed.Host, canonical.Host)
	loopback := environment == EnvironmentTest && (parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1")
	if canonicalOrigin && parsed.Scheme == "https" {
		return nil
	}
	if loopback && (parsed.Scheme == "https" || parsed.Scheme == "http") {
		return nil
	}
	return errors.New("mupag: base URL is not allowed for selected environment")
}

func validateIdempotencyKey(key string) error {
	if len(key) < 1 || len(key) > 128 {
		return errors.New("mupag: invalid idempotency key length")
	}
	for _, character := range []byte(key) {
		if character < 0x21 || character > 0x7e {
			return errors.New("mupag: idempotency key must contain visible ASCII without spaces")
		}
	}
	return nil
}

func isMutation(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func retryableError(err error) bool {
	var conflictErr *unreadableConflictError
	if errors.As(err, &conflictErr) {
		return true
	}
	var networkErr *transportError
	if errors.As(err, &networkErr) {
		return true
	}
	var apiErr *APIError
	if !AsAPIError(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusRequestTimeout ||
		apiErr.StatusCode == http.StatusTooEarly ||
		apiErr.StatusCode == http.StatusTooManyRequests ||
		(apiErr.StatusCode == http.StatusConflict && apiErr.Code == "idempotency_in_progress") ||
		apiErr.StatusCode >= http.StatusInternalServerError
}

func ambiguousMutationError(err error) bool {
	var conflictErr *unreadableConflictError
	if errors.As(err, &conflictErr) {
		return true
	}
	var transportErr *transportError
	if errors.As(err, &transportErr) {
		return true
	}
	var responseErr *ambiguousResponseError
	if errors.As(err, &responseErr) {
		return true
	}
	var apiErr *APIError
	if !AsAPIError(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusRequestTimeout ||
		apiErr.StatusCode == http.StatusTooEarly ||
		apiErr.StatusCode >= http.StatusInternalServerError ||
		(apiErr.StatusCode == http.StatusConflict &&
			(apiErr.Code == "idempotency_in_progress" || apiErr.Code == "idempotency_outcome_unknown"))
}

func idempotencyOutcomeUnknown(err error) bool {
	var apiErr *APIError
	return AsAPIError(err, &apiErr) &&
		apiErr.StatusCode == http.StatusConflict &&
		apiErr.Code == "idempotency_outcome_unknown"
}

func mutationOutcomeError(method, idempotencyKey string, err error) error {
	if !isMutation(method) || idempotencyKey == "" || err == nil {
		return err
	}
	var transportErr *transportError
	if errors.As(err, &transportErr) {
		return &OutcomeUnknownError{IdempotencyKey: idempotencyKey, cause: err}
	}
	var responseErr *ambiguousResponseError
	if errors.As(err, &responseErr) {
		return &OutcomeUnknownError{IdempotencyKey: idempotencyKey, cause: err}
	}
	var conflictErr *unreadableConflictError
	if errors.As(err, &conflictErr) {
		return &OutcomeUnknownError{IdempotencyKey: idempotencyKey, cause: err}
	}
	var apiErr *APIError
	if AsAPIError(err, &apiErr) && (apiErr.StatusCode == http.StatusRequestTimeout ||
		apiErr.StatusCode == http.StatusTooEarly ||
		apiErr.StatusCode >= http.StatusInternalServerError ||
		(apiErr.StatusCode >= http.StatusMultipleChoices && apiErr.StatusCode < http.StatusBadRequest) ||
		(apiErr.StatusCode == http.StatusConflict && apiErr.Code == "idempotency_outcome_unknown")) {
		return &OutcomeUnknownError{IdempotencyKey: idempotencyKey, cause: err}
	}
	return err
}

func retryDelay(policy RetryPolicy, attempt int, err error) time.Duration {
	var rateErr *RateLimitError
	if errors.As(err, &rateErr) && rateErr.RetryAfter > 0 {
		if rateErr.RetryAfter > 30*time.Second {
			return 30 * time.Second
		}
		return rateErr.RetryAfter
	}
	delay := policy.BaseDelay
	for exponent := 0; exponent < attempt && delay < 30*time.Second; exponent++ {
		delay *= 2
	}
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	if delay <= 0 {
		return 0
	}
	// Jitter de 75%..125% evita rajadas sincronizadas sem ultrapassar o teto.
	jitterRange := delay / 2
	jittered := delay - delay/4
	if jitterRange > 0 {
		jittered += time.Duration(mathrand.Int64N(int64(jitterRange) + 1))
	}
	if jittered > 30*time.Second {
		return 30 * time.Second
	}
	return jittered
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func readBoundedBody(response *http.Response, maxBytes int64) ([]byte, error) {
	if header := response.Header.Get("Content-Length"); header != "" {
		length, err := strconv.ParseInt(header, 10, 64)
		if err != nil || length < 0 {
			return nil, errors.New("mupag: response has invalid Content-Length")
		}
		if length > maxBytes {
			return nil, errors.New("mupag: response body exceeds configured limit")
		}
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, &transportError{cause: err}
	}
	if int64(len(contents)) > maxBytes {
		return nil, errors.New("mupag: response body exceeds configured limit")
	}
	return contents, nil
}
