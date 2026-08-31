package mupag_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mupag "github.com/mupaybr/mupag-sdk-go"
)

func TestMutationAmbiguityIsStickyAcrossRetries(t *testing.T) {
	tests := []struct {
		name      string
		responses []roundTripResult
	}{
		{
			name: "server error followed by conflict",
			responses: []roundTripResult{
				{response: jsonHTTPResponse(503, `{"code":"temporarily_unavailable"}`)},
				{response: jsonHTTPResponse(409, `{"code":"fingerprint_conflict"}`)},
			},
		},
		{
			name: "transport loss followed by conflict",
			responses: []roundTripResult{
				{err: errors.New("response lost")},
				{response: jsonHTTPResponse(409, `{"code":"conflict"}`)},
			},
		},
		{
			name: "server error followed by rate limit",
			responses: []roundTripResult{
				{response: jsonHTTPResponse(503, `{"code":"temporarily_unavailable"}`)},
				{response: jsonHTTPResponse(429, `{"code":"rate_limited"}`)},
			},
		},
		{
			name: "in progress followed by fingerprint conflict",
			responses: []roundTripResult{
				{response: jsonHTTPResponse(409, `{"code":"idempotency_in_progress"}`)},
				{response: jsonHTTPResponse(409, `{"code":"fingerprint_conflict"}`)},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var attempts int
			var sentKeys []string
			client := testClientWithResults(t, 1, test.responses, &attempts, &sentKeys)

			_, err := client.Charges.Create(
				context.Background(),
				validPixCharge(),
				mupag.WithIdempotencyKey("sticky-key"),
			)
			var outcomeErr *mupag.OutcomeUnknownError
			if !errors.As(err, &outcomeErr) {
				t.Fatalf("err = %T (%v), want *mupag.OutcomeUnknownError", err, err)
			}
			if outcomeErr.IdempotencyKey != "sticky-key" {
				t.Fatalf("idempotency key = %q", outcomeErr.IdempotencyKey)
			}
			if attempts != 2 || len(sentKeys) != 2 || sentKeys[0] != sentKeys[1] {
				t.Fatalf("attempts = %d, keys = %#v", attempts, sentKeys)
			}
		})
	}
}

func TestMutationAmbiguousStatusMatrix(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusTooEarly} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var attempts int
			client := testClientWithResults(
				t,
				0,
				[]roundTripResult{{response: jsonHTTPResponse(status, `{"code":"transient"}`)}},
				&attempts,
				nil,
			)
			_, err := client.Charges.Create(context.Background(), validPixCharge())
			var outcomeErr *mupag.OutcomeUnknownError
			if !errors.As(err, &outcomeErr) {
				t.Fatalf("status %d err = %T (%v), want outcome unknown", status, err, err)
			}
			if outcomeErr.IdempotencyKey == "" || attempts != 1 {
				t.Fatalf("outcome = %#v, attempts = %d", outcomeErr, attempts)
			}
		})
	}
}

func TestMutationIdempotencyConflictCodesHaveDistinctSemantics(t *testing.T) {
	t.Run("in progress is retried with the same key", func(t *testing.T) {
		var attempts int
		var sentKeys []string
		client := testClientWithResults(
			t,
			1,
			[]roundTripResult{
				{response: jsonHTTPResponse(409, `{"code":"idempotency_in_progress"}`)},
				{response: jsonHTTPResponse(201, validChargeJSON())},
			},
			&attempts,
			&sentKeys,
		)
		charge, err := client.Charges.Create(context.Background(), validPixCharge())
		if err != nil || charge == nil || charge.ChargeID != "ch_1" {
			t.Fatalf("charge = %#v, err = %v", charge, err)
		}
		if attempts != 2 || sentKeys[0] == "" || sentKeys[0] != sentKeys[1] {
			t.Fatalf("attempts = %d, keys = %#v", attempts, sentKeys)
		}
	})

	t.Run("outcome unknown is immediate", func(t *testing.T) {
		var attempts int
		client := testClientWithResults(
			t,
			3,
			[]roundTripResult{{response: jsonHTTPResponse(409, `{"code":"idempotency_outcome_unknown"}`)}},
			&attempts,
			nil,
		)
		_, err := client.Charges.Create(context.Background(), validPixCharge())
		var outcomeErr *mupag.OutcomeUnknownError
		if !errors.As(err, &outcomeErr) || attempts != 1 {
			t.Fatalf("err = %T (%v), attempts = %d", err, err, attempts)
		}
	})

	t.Run("in progress exhaustion is unknown", func(t *testing.T) {
		var attempts int
		client := testClientWithResults(
			t,
			0,
			[]roundTripResult{{response: jsonHTTPResponse(409, `{"code":"idempotency_in_progress"}`)}},
			&attempts,
			nil,
		)
		_, err := client.Charges.Create(context.Background(), validPixCharge())
		var outcomeErr *mupag.OutcomeUnknownError
		if !errors.As(err, &outcomeErr) || attempts != 1 {
			t.Fatalf("err = %T (%v), attempts = %d", err, err, attempts)
		}
	})

	t.Run("fingerprint conflict is definitive without prior ambiguity", func(t *testing.T) {
		var attempts int
		client := testClientWithResults(
			t,
			3,
			[]roundTripResult{{response: jsonHTTPResponse(409, `{"code":"fingerprint_conflict"}`)}},
			&attempts,
			nil,
		)
		_, err := client.Charges.Create(context.Background(), validPixCharge())
		var outcomeErr *mupag.OutcomeUnknownError
		if errors.As(err, &outcomeErr) {
			t.Fatalf("fingerprint conflict reported unknown: %v", err)
		}
		var apiErr *mupag.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != "fingerprint_conflict" || attempts != 1 {
			t.Fatalf("api error = %#v, attempts = %d", apiErr, attempts)
		}
	})
}

func TestMutationReadableConflictRequiresUsableCode(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantUnknown bool
		wantCode    string
	}{
		{name: "malformed JSON", body: `{`, wantUnknown: true},
		{name: "missing code", body: `{}`, wantUnknown: true},
		{name: "empty code", body: `{"code":""}`, wantUnknown: true},
		{name: "fallback code", body: `{"code":"http_409"}`, wantUnknown: true},
		{name: "recognized fingerprint conflict", body: `{"code":"fingerprint_conflict"}`, wantCode: "fingerprint_conflict"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var attempts int
			client := testClientWithResults(
				t,
				0,
				[]roundTripResult{{response: jsonHTTPResponse(http.StatusConflict, test.body)}},
				&attempts,
				nil,
			)

			_, err := client.Charges.Create(
				context.Background(),
				validPixCharge(),
				mupag.WithIdempotencyKey("readable-conflict-key"),
			)
			var outcomeErr *mupag.OutcomeUnknownError
			if test.wantUnknown {
				if !errors.As(err, &outcomeErr) {
					t.Fatalf("err = %T (%v), want outcome unknown", err, err)
				}
				if outcomeErr.IdempotencyKey != "readable-conflict-key" {
					t.Fatalf("idempotency key = %q", outcomeErr.IdempotencyKey)
				}
			} else {
				if errors.As(err, &outcomeErr) {
					t.Fatalf("recognized conflict reported unknown: %v", err)
				}
				var apiErr *mupag.APIError
				if !errors.As(err, &apiErr) || apiErr.Code != test.wantCode {
					t.Fatalf("api error = %#v, want code %q", apiErr, test.wantCode)
				}
			}
			if attempts != 1 {
				t.Fatalf("attempts = %d, want 1", attempts)
			}
		})
	}
}

func TestMutationOnlyValid2xxConfirmsOutcome(t *testing.T) {
	tests := []struct {
		name     string
		response *http.Response
	}{
		{name: "redirect", response: jsonHTTPResponse(302, `{}`)},
		{name: "empty body", response: jsonHTTPResponse(201, ``)},
		{name: "empty object", response: jsonHTTPResponse(201, `{}`)},
		{name: "malformed JSON", response: jsonHTTPResponse(201, `{`)},
		{name: "invalid economics", response: jsonHTTPResponse(201, `{"charge_id":"ch_1","status":"pending","amount_cents":-1}`)},
		{name: "amount below minimum", response: jsonHTTPResponse(201, `{"charge_id":"ch_1","status":"pending","amount_cents":99}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var attempts int
			client := testClientWithResults(
				t,
				0,
				[]roundTripResult{{response: test.response}},
				&attempts,
				nil,
			)
			_, err := client.Charges.Create(context.Background(), validPixCharge())
			var outcomeErr *mupag.OutcomeUnknownError
			if !errors.As(err, &outcomeErr) {
				t.Fatalf("err = %T (%v), want outcome unknown", err, err)
			}
			if outcomeErr.IdempotencyKey == "" {
				t.Fatal("outcome unknown did not expose the effective key")
			}
		})
	}
}

func TestUnreadableRetryableStatusRetriesWithSameKey(t *testing.T) {
	for _, status := range []int{
		http.StatusRequestTimeout,
		http.StatusTooEarly,
		http.StatusTooManyRequests,
		http.StatusServiceUnavailable,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var attempts int
			var sentKeys []string
			client := testClientWithResults(
				t,
				1,
				[]roundTripResult{
					{response: oversizedHTTPResponse(status)},
					{response: jsonHTTPResponse(201, validChargeJSON())},
				},
				&attempts,
				&sentKeys,
				mupag.WithMaxResponseBytes(128),
			)

			charge, err := client.Charges.Create(
				context.Background(),
				validPixCharge(),
				mupag.WithIdempotencyKey("unreadable-retry-key"),
			)
			if err != nil || charge == nil || charge.ChargeID != "ch_1" {
				t.Fatalf("charge = %#v, err = %v", charge, err)
			}
			if attempts != 2 || sentKeys[0] != sentKeys[1] {
				t.Fatalf("attempts = %d, keys = %#v", attempts, sentKeys)
			}
		})
	}
}

func TestUnreadableConflictIsUnknownBecauseCodeCannotBeClassified(t *testing.T) {
	var attempts int
	client := testClientWithResults(
		t,
		0,
		[]roundTripResult{{response: oversizedHTTPResponse(http.StatusConflict)}},
		&attempts,
		nil,
		mupag.WithMaxResponseBytes(128),
	)

	_, err := client.Charges.Create(
		context.Background(),
		validPixCharge(),
		mupag.WithIdempotencyKey("unreadable-conflict-key"),
	)
	var outcomeErr *mupag.OutcomeUnknownError
	if !errors.As(err, &outcomeErr) {
		t.Fatalf("err = %T (%v), want outcome unknown", err, err)
	}
	if outcomeErr.IdempotencyKey != "unreadable-conflict-key" || attempts != 1 {
		t.Fatalf("outcome = %#v, attempts = %d", outcomeErr, attempts)
	}
}

func TestMutationDoesNotFollowRedirects(t *testing.T) {
	var redirectedRequests int
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/redirected" {
			redirectedRequests++
			response.Header().Set("Content-Type", "application/json")
			response.WriteHeader(http.StatusCreated)
			_, _ = response.Write([]byte(validChargeJSON()))
			return
		}
		response.Header().Set("Location", "/redirected")
		response.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithBaseURL(server.URL),
		mupag.WithRetryPolicy(mupag.RetryPolicy{MaxRetries: 0}),
	)
	_, err := client.Charges.Create(context.Background(), validPixCharge())
	var outcomeErr *mupag.OutcomeUnknownError
	if !errors.As(err, &outcomeErr) {
		t.Fatalf("err = %T (%v), want outcome unknown", err, err)
	}
	if redirectedRequests != 0 {
		t.Fatalf("redirected requests = %d, want zero", redirectedRequests)
	}
}

type roundTripResult struct {
	response *http.Response
	err      error
}

func testClientWithResults(
	t *testing.T,
	maxRetries int,
	results []roundTripResult,
	attempts *int,
	sentKeys *[]string,
	options ...mupag.Option,
) *mupag.Client {
	t.Helper()
	clientOptions := []mupag.Option{
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithRetryPolicy(mupag.RetryPolicy{MaxRetries: maxRetries}),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			index := *attempts
			*attempts++
			if sentKeys != nil {
				*sentKeys = append(*sentKeys, request.Header.Get("Idempotency-Key"))
			}
			if index >= len(results) {
				t.Fatalf("unexpected request %d", index+1)
			}
			return results[index].response, results[index].err
		})}),
	}
	clientOptions = append(clientOptions, options...)
	return mupag.NewClient(clientOptions...)
}

func jsonHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func oversizedHTTPResponse(status int) *http.Response {
	response := jsonHTTPResponse(status, strings.Repeat("x", 256))
	response.Header.Set("Content-Length", "256")
	return response
}

func validChargeJSON() string {
	return `{"charge_id":"ch_1","status":"pending","amount_cents":100,"psp_charge_id":"psp_1"}`
}
