package mupag_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	mupag "github.com/mupaybr/mupag-sdk-go"
)

func TestMutationTransportFailureExposesUnknownOutcomeAndSentIdempotencyKey(t *testing.T) {
	var sentKey string
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithRetryPolicy(mupag.RetryPolicy{MaxRetries: 0}),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			sentKey = request.Header.Get("Idempotency-Key")
			return nil, errors.New("response lost after request dispatch")
		})}),
	)

	_, err := client.Charges.Create(context.Background(), validPixCharge())
	var outcomeErr *mupag.OutcomeUnknownError
	if !errors.As(err, &outcomeErr) {
		t.Fatalf("err = %T, want *mupag.OutcomeUnknownError", err)
	}
	if sentKey == "" || outcomeErr.IdempotencyKey != sentKey {
		t.Fatalf("sent key = %q, exposed key = %q", sentKey, outcomeErr.IdempotencyKey)
	}
	if errors.Unwrap(outcomeErr) == nil {
		t.Fatal("outcome error must preserve the transport cause")
	}
}

func TestMutationExhaustedServerErrorExposesUnknownOutcome(t *testing.T) {
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithRetryPolicy(mupag.RetryPolicy{MaxRetries: 0}),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Header:     http.Header{"Content-Type": []string{"application/problem+json"}},
				Body:       io.NopCloser(strings.NewReader(`{"code":"temporarily_unavailable","request_id":"req_503"}`)),
			}, nil
		})}),
	)

	_, err := client.Charges.Create(
		context.Background(),
		validPixCharge(),
		mupag.WithIdempotencyKey("order_503_attempt_1"),
	)
	var outcomeErr *mupag.OutcomeUnknownError
	if !errors.As(err, &outcomeErr) || outcomeErr.IdempotencyKey != "order_503_attempt_1" {
		t.Fatalf("outcome error = %#v", outcomeErr)
	}
	var apiErr *mupag.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("underlying API error = %#v", apiErr)
	}
}

func TestMutationDefinitiveClientErrorDoesNotReportUnknownOutcome(t *testing.T) {
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithRetryPolicy(mupag.RetryPolicy{MaxRetries: 0}),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusUnprocessableEntity,
				Header:     http.Header{"Content-Type": []string{"application/problem+json"}},
				Body:       io.NopCloser(strings.NewReader(`{"code":"invalid_amount","request_id":"req_422"}`)),
			}, nil
		})}),
	)

	_, err := client.Charges.Create(context.Background(), validPixCharge())
	var outcomeErr *mupag.OutcomeUnknownError
	if errors.As(err, &outcomeErr) {
		t.Fatalf("definitive 422 was reported as unknown outcome: %v", err)
	}
	var apiErr *mupag.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("API error = %#v", apiErr)
	}
}

func TestMutationPreDispatchContextErrorDoesNotReportUnknownOutcome(t *testing.T) {
	tests := []struct {
		name       string
		newContext func() (context.Context, context.CancelFunc)
		want       error
	}{
		{
			name: "canceled",
			newContext: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
			want: context.Canceled,
		},
		{
			name: "expired",
			newContext: func() (context.Context, context.CancelFunc) {
				return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			},
			want: context.DeadlineExceeded,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := test.newContext()
			defer cancel()
			requests := 0
			client := mupag.NewClient(
				mupag.WithAPIKey("sk_test_123"),
				mupag.WithTestEnvironment(),
				mupag.WithRetryPolicy(mupag.RetryPolicy{MaxRetries: 1}),
				mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					requests++
					return nil, errors.New("transport must not be called")
				})}),
			)

			_, err := client.Charges.Create(
				ctx,
				validPixCharge(),
				mupag.WithIdempotencyKey("pre-dispatch-"+test.name),
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("err = %T (%v), want %v", err, err, test.want)
			}
			var outcomeErr *mupag.OutcomeUnknownError
			if errors.As(err, &outcomeErr) {
				t.Fatalf("pre-dispatch context error was reported as unknown outcome: %v", err)
			}
			if requests != 0 {
				t.Fatalf("transport requests = %d, want 0", requests)
			}
		})
	}
}

func TestMutationContextCancellationAfterDispatchReportsUnknownOutcome(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requests := 0
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithRetryPolicy(mupag.RetryPolicy{MaxRetries: 0}),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			cancel()
			return nil, request.Context().Err()
		})}),
	)

	_, err := client.Charges.Create(
		ctx,
		validPixCharge(),
		mupag.WithIdempotencyKey("dispatch-started-context-canceled"),
	)
	var outcomeErr *mupag.OutcomeUnknownError
	if !errors.As(err, &outcomeErr) {
		t.Fatalf("err = %T (%v), want *mupag.OutcomeUnknownError", err, err)
	}
	if outcomeErr.IdempotencyKey != "dispatch-started-context-canceled" {
		t.Fatalf("idempotency key = %q", outcomeErr.IdempotencyKey)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled cause", err)
	}
	if requests != 1 {
		t.Fatalf("transport requests = %d, want 1", requests)
	}
}

func TestReadRetryCancellationPreservesContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithRetryPolicy(mupag.RetryPolicy{MaxRetries: 1}),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("temporary transport failure")
		})}),
	)

	_, err := client.Charges.List(ctx, mupag.ChargeListParams{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
