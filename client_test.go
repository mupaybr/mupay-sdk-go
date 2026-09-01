package mupag_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mupag "github.com/mupaybr/mupag-sdk-go"
)

func TestChargesCreateGeneratesIdempotencyKeyAndPostsJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/charges" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk_test_123" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("Idempotency-Key"); len(got) <= len("sdk-go-") || got[:len("sdk-go-")] != "sdk-go-" {
			t.Fatalf("Idempotency-Key = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["payment_method"] != "pix" || body["amount_cents"] != float64(9900) {
			t.Fatalf("body = %v", body)
		}
		writeJSON(w, http.StatusCreated, `{"charge_id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa","amount_cents":9900,"psp_charge_id":"woovi_001","pix_emv_code":"000201010212","status":"pending"}`)
	}))
	defer server.Close()

	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithBaseURL(server.URL),
		mupag.WithRetryPolicy(mupag.RetryPolicy{MaxRetries: 0}),
	)

	charge, err := client.Charges.Create(context.Background(), mupag.ChargeCreateParams{
		AmountCents:   9900,
		PaymentMethod: "pix",
		Customer: mupag.CustomerParams{
			Name:  "Ana Silva",
			Email: "ana@example.test",
			TaxID: "12345678901",
		},
	})
	if err != nil {
		t.Fatalf("create charge: %v", err)
	}
	if charge.ChargeID != "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa" || charge.AmountCents != 9900 || charge.PixEMVCode != "000201010212" {
		t.Fatalf("charge = %+v", charge)
	}
}

func TestChargesCreatePreservesExplicitIdempotencyKey(t *testing.T) {
	var gotKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("Idempotency-Key")
		writeJSON(w, http.StatusCreated, `{"charge_id":"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb","amount_cents":14990,"psp_charge_id":"woovi_002","status":"pending"}`)
	}))
	defer server.Close()

	client := newTestClient(server.URL)

	_, err := client.Charges.Create(
		context.Background(),
		mupag.ChargeCreateParams{AmountCents: 14990, PaymentMethod: "pix", Customer: validCustomer()},
		mupag.WithIdempotencyKey("order_456_charge_1"),
	)
	if err != nil {
		t.Fatalf("create charge: %v", err)
	}
	if gotKey != "order_456_charge_1" {
		t.Fatalf("Idempotency-Key = %q", gotKey)
	}
}

func TestChargeCreateRejectsMoneyOutsideSupportedRangeBeforeNetwork(t *testing.T) {
	requests := 0
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return nil, errors.New("network must not be reached")
		})}),
	)

	for _, amount := range []int64{99, 9_000_000_000_000_001} {
		_, err := client.Charges.Create(context.Background(), mupag.ChargeCreateParams{
			AmountCents:   amount,
			PaymentMethod: "pix",
			Customer:      validCustomer(),
		})
		if err == nil {
			t.Fatalf("amount %d outside supported range was accepted", amount)
		}
	}
	if requests != 0 {
		t.Fatalf("network requests = %d, want 0", requests)
	}
}

func TestChargeCreateAcceptsMoneyBoundaryAmounts(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		var body mupag.ChargeCreateParams
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		writeJSON(writer, http.StatusCreated, fmt.Sprintf(
			`{"charge_id":"charge_%d","amount_cents":%d,"status":"pending"}`,
			requests,
			body.AmountCents,
		))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	for _, amount := range []int64{100, 9_000_000_000_000_000} {
		charge, err := client.Charges.Create(context.Background(), mupag.ChargeCreateParams{
			AmountCents:   amount,
			PaymentMethod: "pix",
			Customer:      validCustomer(),
		})
		if err != nil {
			t.Fatalf("amount %d rejected: %v", amount, err)
		}
		if charge.AmountCents != amount {
			t.Fatalf("response amount = %d, want %d", charge.AmountCents, amount)
		}
	}
	if requests != 2 {
		t.Fatalf("network requests = %d, want 2", requests)
	}
}

func TestChargeCreateValidatesAggregateSplitAllocation(t *testing.T) {
	const maximumAmountCents int64 = 9_000_000_000_000_000
	tests := []struct {
		name        string
		amountCents int64
		rules       []mupag.SplitRuleParams
		wantError   bool
	}{
		{
			name:        "fixed only exact boundary",
			amountCents: 1_000,
			rules: []mupag.SplitRuleParams{
				{RecipientID: "recipient_1", ValueType: "fixed_amount", ValueCents: 400},
				{RecipientID: "recipient_2", ValueType: "fixed_amount", ValueCents: 600},
			},
		},
		{
			name:        "fixed only above charge",
			amountCents: 1_000,
			rules: []mupag.SplitRuleParams{
				{RecipientID: "recipient_1", ValueType: "fixed_amount", ValueCents: 400},
				{RecipientID: "recipient_2", ValueType: "fixed_amount", ValueCents: 601},
			},
			wantError: true,
		},
		{
			name:        "percentage only exact boundary",
			amountCents: 1_000,
			rules: []mupag.SplitRuleParams{
				{RecipientID: "recipient_1", ValueType: "percentage_of_gross", ValueBPS: 4_000},
				{RecipientID: "recipient_2", ValueType: "percentage_of_gross", ValueBPS: 6_000},
			},
		},
		{
			name:        "percentage only above one hundred percent",
			amountCents: 1_000,
			rules: []mupag.SplitRuleParams{
				{RecipientID: "recipient_1", ValueType: "percentage_of_gross", ValueBPS: 4_000},
				{RecipientID: "recipient_2", ValueType: "percentage_of_gross", ValueBPS: 6_001},
			},
			wantError: true,
		},
		{
			name:        "mixed exact rounded boundary",
			amountCents: 1_001,
			rules: []mupag.SplitRuleParams{
				{RecipientID: "recipient_1", ValueType: "fixed_amount", ValueCents: 501},
				{RecipientID: "recipient_2", ValueType: "percentage_of_gross", ValueBPS: 5_000},
			},
		},
		{
			name:        "mixed above charge",
			amountCents: 1_001,
			rules: []mupag.SplitRuleParams{
				{RecipientID: "recipient_1", ValueType: "fixed_amount", ValueCents: 502},
				{RecipientID: "recipient_2", ValueType: "percentage_of_gross", ValueBPS: 5_000},
			},
			wantError: true,
		},
		{
			name:        "maximum monetary exact boundary without overflow",
			amountCents: maximumAmountCents,
			rules: []mupag.SplitRuleParams{
				{RecipientID: "recipient_1", ValueType: "fixed_amount", ValueCents: maximumAmountCents / 2},
				{RecipientID: "recipient_2", ValueType: "percentage_of_gross", ValueBPS: 5_000},
			},
		},
		{
			name:        "maximum monetary above charge without overflow",
			amountCents: maximumAmountCents,
			rules: []mupag.SplitRuleParams{
				{RecipientID: "recipient_1", ValueType: "fixed_amount", ValueCents: maximumAmountCents/2 + 1},
				{RecipientID: "recipient_2", ValueType: "percentage_of_gross", ValueBPS: 5_000},
			},
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			client := mupag.NewClient(
				mupag.WithAPIKey("sk_test_123"),
				mupag.WithTestEnvironment(),
				mupag.WithRetryPolicy(mupag.RetryPolicy{MaxRetries: 0}),
				mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					requests++
					return jsonHTTPResponse(http.StatusCreated, fmt.Sprintf(
						`{"charge_id":"charge_1","status":"pending","amount_cents":%d}`,
						test.amountCents,
					)), nil
				})}),
			)

			params := validPixCharge()
			params.AmountCents = test.amountCents
			params.SplitRules = test.rules
			charge, err := client.Charges.Create(context.Background(), params)
			if test.wantError {
				if err == nil {
					t.Fatalf("aggregate split was accepted: %#v", charge)
				}
				if requests != 0 {
					t.Fatalf("network requests = %d, want 0", requests)
				}
				return
			}
			if err != nil {
				t.Fatalf("valid aggregate split returned error: %v", err)
			}
			if charge == nil || charge.AmountCents != test.amountCents {
				t.Fatalf("charge = %#v, want amount %d", charge, test.amountCents)
			}
			if requests != 1 {
				t.Fatalf("network requests = %d, want 1", requests)
			}
		})
	}
}

func TestCardChargeForwardsLiteralPayerIPAndSingleInstallment(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		writeJSON(w, http.StatusCreated, `{"charge_id":"card_1","amount_cents":14990,"psp_charge_id":"asaas_1","status":"pending"}`)
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	_, err := client.Charges.Create(context.Background(), mupag.ChargeCreateParams{
		AmountCents:            14990,
		PaymentMethod:          "credit_card",
		Customer:               validCustomer(),
		CardTokenID:            "token_123",
		PayerIP:                "2001:db8::1",
		Installments:           1,
		ProductMaxInstallments: 1,
	})
	if err != nil {
		t.Fatalf("create card charge: %v", err)
	}
	if body["payer_ip"] != "2001:db8::1" || body["installments"] != float64(1) || body["product_max_installments"] != float64(1) {
		t.Fatalf("body = %v", body)
	}
}

func TestAPIProblemDetailsBecomesTypedError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusUnprocessableEntity, `{"code":"invalid_amount","request_id":"req_123","detail":"amount must be positive"}`)
	}))
	defer server.Close()

	client := newTestClient(server.URL)

	_, err := client.Charges.Create(context.Background(), mupag.ChargeCreateParams{
		AmountCents: 100, PaymentMethod: "pix", Customer: validCustomer(),
	})
	var apiErr *mupag.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %T, want *mupag.APIError", err)
	}
	if apiErr.StatusCode != http.StatusUnprocessableEntity || apiErr.Code != "invalid_amount" || apiErr.RequestID != "req_123" {
		t.Fatalf("apiErr = %+v", apiErr)
	}
}

func TestRateLimitErrorIncludesRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		writeJSON(w, http.StatusTooManyRequests, `{"code":"rate_limited","request_id":"req_rate"}`)
	}))
	defer server.Close()

	client := newTestClient(server.URL)

	_, err := client.Subscriptions.Cancel(context.Background(), "sub_123", mupag.CancelSubscriptionParams{Mode: "immediate"}, mupag.WithIdempotencyKey("cancel_sub_123"))
	var rateErr *mupag.RateLimitError
	if !errors.As(err, &rateErr) {
		t.Fatalf("err = %T, want *mupag.RateLimitError", err)
	}
	if rateErr.RetryAfter != 7*time.Second {
		t.Fatalf("RetryAfter = %s", rateErr.RetryAfter)
	}
}

func TestRetriesTransientServerErrorThenReturnsSuccess(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			writeJSON(w, http.StatusInternalServerError, `{"code":"internal_error","request_id":"req_fail"}`)
			return
		}
		writeJSON(w, http.StatusCreated, `{"charge_id":"cccccccc-cccc-cccc-cccc-cccccccccccc","amount_cents":5000,"psp_charge_id":"woovi_retry","status":"pending"}`)
	}))
	defer server.Close()

	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithBaseURL(server.URL),
		mupag.WithRetryPolicy(mupag.RetryPolicy{MaxRetries: 1, BaseDelay: 0}),
	)

	charge, err := client.Charges.Create(context.Background(), mupag.ChargeCreateParams{
		AmountCents:   5000,
		PaymentMethod: "pix",
		Customer:      validCustomer(),
	})
	if err != nil {
		t.Fatalf("create charge: %v", err)
	}
	if charge.ChargeID != "cccccccc-cccc-cccc-cccc-cccccccccccc" || attempts != 2 {
		t.Fatalf("charge=%+v attempts=%d", charge, attempts)
	}
}

func TestSubscriptionsCancelPostsCancelRequest(t *testing.T) {
	var gotPath string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		writeJSON(w, http.StatusOK, `{"id":"sub_123","status":"canceled","cancel_at_period_end":false}`)
	}))
	defer server.Close()

	client := newTestClient(server.URL)

	subscription, err := client.Subscriptions.Cancel(
		context.Background(),
		"sub_123",
		mupag.CancelSubscriptionParams{Mode: "immediate", Reason: "pedido do cliente"},
		mupag.WithIdempotencyKey("cancel_sub_123"),
	)
	if err != nil {
		t.Fatalf("cancel subscription: %v", err)
	}
	if gotPath != "/v1/subscriptions/sub_123/cancel" || subscription.Status != "canceled" || body["mode"] != "immediate" {
		t.Fatalf("path=%q subscription=%+v body=%v", gotPath, subscription, body)
	}
}

func TestClientCanUseSandboxEnvironmentWithCustomHTTPClient(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.sandbox.mupag.com.br" {
			t.Fatalf("host = %s", r.URL.Host)
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"charge_id":"dddddddd-dddd-dddd-dddd-dddddddddddd","amount_cents":100,"psp_charge_id":"woovi_env","status":"pending"}`)),
		}, nil
	})
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithHTTPClient(&http.Client{Transport: transport}),
		mupag.WithRetryPolicy(mupag.RetryPolicy{MaxRetries: 0}),
	)

	charge, err := client.Charges.Create(context.Background(), mupag.ChargeCreateParams{
		AmountCents:   100,
		PaymentMethod: "pix",
		Customer:      validCustomer(),
	})
	if err != nil {
		t.Fatalf("create charge: %v", err)
	}
	if charge.ChargeID != "dddddddd-dddd-dddd-dddd-dddddddddddd" {
		t.Fatalf("charge = %+v", charge)
	}
}

func TestTypedErrorMessagesAreSanitized(t *testing.T) {
	apiErr := &mupag.APIError{StatusCode: 422, Code: "invalid_amount", RequestID: "req_123"}
	if got := apiErr.Error(); !strings.Contains(got, "invalid_amount") || !strings.Contains(got, "req_123") {
		t.Fatalf("api error string = %q", got)
	}

	rateErr := &mupag.RateLimitError{APIError: *apiErr, RetryAfter: 7 * time.Second}
	if got := rateErr.Error(); !strings.Contains(got, "invalid_amount") {
		t.Fatalf("rate error string = %q", got)
	}
	var unwrapped *mupag.APIError
	if !errors.As(rateErr, &unwrapped) {
		t.Fatal("expected RateLimitError to unwrap as APIError")
	}
}

func newTestClient(baseURL string) *mupag.Client {
	return mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithBaseURL(baseURL),
		mupag.WithRetryPolicy(mupag.RetryPolicy{MaxRetries: 0}),
	)
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}
