package mupag

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// APIError preserva status HTTP, codigo estavel e request_id retornados pela API.
type APIError struct {
	StatusCode int
	Code       string
	RequestID  string
	Detail     string
}

// OutcomeUnknownError informa que uma mutacao pode ter sido aceita, mas a
// resposta final nao chegou ao SDK. Reutilize IdempotencyKey com o mesmo
// payload para consultar ou repetir a mesma operacao sem cria-la novamente.
type OutcomeUnknownError struct {
	IdempotencyKey string
	cause          error
}

// Error evita incluir a chave em logs por acidente; ela continua disponivel
// no campo estruturado IdempotencyKey.
func (err *OutcomeUnknownError) Error() string {
	if err == nil || err.cause == nil {
		return "mupag: mutation outcome is unknown; reuse the exposed idempotency key with the same payload"
	}
	return fmt.Sprintf("mupag: mutation outcome is unknown; reuse the exposed idempotency key with the same payload: %v", err.cause)
}

// Unwrap preserva o erro de transporte ou API que tornou o resultado ambiguo.
func (err *OutcomeUnknownError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

// Error resume o erro sem incluir payload sensivel.
func (err *APIError) Error() string {
	if err == nil {
		return ""
	}
	if err.Code != "" {
		return fmt.Sprintf("mupag api error: status=%d code=%s request_id=%s", err.StatusCode, err.Code, err.RequestID)
	}
	return fmt.Sprintf("mupag api error: status=%d", err.StatusCode)
}

// RateLimitError adiciona Retry-After ao erro tipado de HTTP 429.
type RateLimitError struct {
	APIError
	RetryAfter time.Duration
}

// Error resume rate limit mantendo compatibilidade com errors.As para APIError.
func (err *RateLimitError) Error() string {
	return err.APIError.Error()
}

// Unwrap permite tratar 429 tambem como APIError generico via errors.As.
func (err *RateLimitError) Unwrap() error {
	return &err.APIError
}

// AsAPIError centraliza unwrap para retry e consumidores que tratam APIError.
func AsAPIError(err error, target **APIError) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		*target = apiErr
		return true
	}
	var rateErr *RateLimitError
	if errors.As(err, &rateErr) {
		*target = &rateErr.APIError
		return true
	}
	return false
}

func decodeAPIError(statusCode int, headers http.Header, body []byte) error {
	problem := struct {
		Code      string `json:"code"`
		RequestID string `json:"request_id"`
		Detail    string `json:"detail"`
	}{}
	_ = json.Unmarshal(body, &problem)
	apiErr := APIError{
		StatusCode: statusCode,
		Code:       problem.Code,
		RequestID:  problem.RequestID,
		Detail:     problem.Detail,
	}
	if apiErr.Code == "" {
		apiErr.Code = fmt.Sprintf("http_%d", statusCode)
	}
	if statusCode == http.StatusTooManyRequests {
		return &RateLimitError{
			APIError:   apiErr,
			RetryAfter: retryAfter(headers.Get("Retry-After")),
		}
	}
	return &apiErr
}

func retryAfter(value string) time.Duration {
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0
		}
		if seconds > 30 {
			seconds = 30
		}
		return time.Duration(seconds) * time.Second
	}
	retryAt, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	delay := time.Until(retryAt)
	if delay <= 0 {
		return 0
	}
	if delay > 30*time.Second {
		return 30 * time.Second
	}
	return delay
}
