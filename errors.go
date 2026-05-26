package mupaysdk

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

// Error resume o erro sem incluir payload sensivel.
func (err *APIError) Error() string {
	if err == nil {
		return ""
	}
	if err.Code != "" {
		return fmt.Sprintf("mupay api error: status=%d code=%s request_id=%s", err.StatusCode, err.Code, err.RequestID)
	}
	return fmt.Sprintf("mupay api error: status=%d", err.StatusCode)
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
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
