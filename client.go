// Package mupaysdk fornece um SDK Go pequeno para consumir a API publica da Mupay.
package mupaysdk

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://api.mupay.com.br"
	testBaseURL    = "https://api.sandbox.mupay.com.br"
)

// Client concentra os recursos publicos da API sem guardar estado mutavel global.
type Client struct {
	apiKey      string
	baseURL     string
	httpClient  *http.Client
	retryPolicy RetryPolicy

	Charges       *ChargesService
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
		baseURL:     defaultBaseURL,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
		retryPolicy: RetryPolicy{MaxRetries: 2, BaseDelay: 200 * time.Millisecond},
	}
	for _, opt := range opts {
		opt(client)
	}
	client.baseURL = strings.TrimRight(client.baseURL, "/")
	client.Charges = &ChargesService{client: client}
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

// WithTestEnvironment usa o endpoint sandbox oficial da Mupay.
func WithTestEnvironment() Option {
	return WithBaseURL(testBaseURL)
}

// WithHTTPClient troca o client HTTP mantendo a serializacao e erros do SDK.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(client *Client) {
		if httpClient != nil {
			client.httpClient = httpClient
		}
	}
}

// WithRetryPolicy configura retry em 5xx; 4xx nunca sao repetidos.
func WithRetryPolicy(policy RetryPolicy) Option {
	return func(client *Client) {
		client.retryPolicy = policy
	}
}

type requestOptions struct {
	idempotencyKey string
}

// RequestOption ajusta uma chamada sem mudar o client compartilhado.
type RequestOption func(*requestOptions)

// WithIdempotencyKey preserva a chave idempotente definida pelo consumidor.
func WithIdempotencyKey(key string) RequestOption {
	return func(options *requestOptions) {
		options.idempotencyKey = key
	}
}

func (client *Client) do(ctx context.Context, method, path string, query url.Values, body any, out any, opts ...RequestOption) error {
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
			return err
		}
	}
	attempts := client.retryPolicy.MaxRetries + 1
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 && client.retryPolicy.BaseDelay > 0 {
			time.Sleep(client.retryPolicy.BaseDelay * time.Duration(attempt))
		}
		err = client.doOnce(ctx, method, path, query, encodedBody, out, requestOptions)
		if err == nil {
			return nil
		}
		lastErr = err
		var apiErr *APIError
		if !AsAPIError(err, &apiErr) || apiErr.StatusCode < 500 || attempt == attempts-1 {
			return err
		}
	}
	return lastErr
}

func (client *Client) doOnce(ctx context.Context, method, path string, query url.Values, body []byte, out any, opts requestOptions) error {
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
	if method == http.MethodPost {
		key := strings.TrimSpace(opts.idempotencyKey)
		if key == "" {
			key = "sdk-go-" + randomHex(16)
		}
		request.Header.Set("Idempotency-Key", key)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode >= 400 {
		return decodeAPIError(response.StatusCode, response.Header, responseBody)
	}
	if out == nil || len(responseBody) == 0 {
		return nil
	}
	return json.Unmarshal(responseBody, out)
}

func randomHex(size int) string {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer)
}
