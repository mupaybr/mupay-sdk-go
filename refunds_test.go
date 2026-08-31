package mupag_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mupag "github.com/mupaybr/mupag-sdk-go"
)

func TestRefundsCreateRequiresExplicitFullOrPartialIntent(t *testing.T) {
	requests := 0
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return nil, errors.New("network must not be reached")
		})}),
	)
	amount := int64(100)

	for _, params := range []mupag.RefundCreateParams{
		{},
		{AmountCents: &amount, Full: true},
		{AmountCents: int64Pointer(0)},
	} {
		if _, err := client.Refunds.Create(context.Background(), "charge_1", params); err == nil {
			t.Fatalf("invalid refund intent accepted: %+v", params)
		}
	}
	if requests != 0 {
		t.Fatalf("network requests = %d, want 0", requests)
	}
}

func TestRefundsCreateForwardsExplicitFullIntentAndIdempotency(t *testing.T) {
	var body map[string]any
	var idempotencyKey string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/charges/charge_1/refunds" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		idempotencyKey = request.Header.Get("Idempotency-Key")
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		writeJSON(writer, http.StatusAccepted, `{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":100,"status":"pending"}`)
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	refund, err := client.Refunds.Create(
		context.Background(),
		"charge_1",
		mupag.RefundCreateParams{Full: true, Reason: "requested_by_customer"},
		mupag.WithIdempotencyKey("refund_order_1"),
	)
	if err != nil {
		t.Fatalf("create refund: %v", err)
	}
	if body["full"] != true || body["amount_cents"] != nil || idempotencyKey != "refund_order_1" {
		t.Fatalf("body=%v idempotency=%q", body, idempotencyKey)
	}
	if refund.RefundID != "refund_1" || refund.Status != "pending" {
		t.Fatalf("refund = %+v", refund)
	}
}

func TestRefundsGetAndListByChargeUseBoundedReadContracts(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		switch request.URL.Path {
		case "/v1/refunds/refund_1":
			if request.Method != http.MethodGet {
				t.Fatalf("get method = %s", request.Method)
			}
			writeJSON(writer, http.StatusOK, `{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":100,"status":"completed"}`)
		case "/v1/charges/charge_1/refunds":
			if request.URL.Query().Get("limit") != "25" || request.URL.Query().Get("cursor") != "cursor_1" {
				t.Fatalf("query = %v", request.URL.Query())
			}
			writeJSON(writer, http.StatusOK, `{"refunds":[{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":100,"status":"completed"}],"next_cursor":"cursor_2"}`)
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	refund, err := client.Refunds.Get(context.Background(), "refund_1")
	if err != nil || refund.Status != "completed" {
		t.Fatalf("refund=%+v err=%v", refund, err)
	}
	page, err := client.Refunds.ListByCharge(
		context.Background(),
		"charge_1",
		mupag.RefundListParams{Limit: 25, Cursor: "cursor_1"},
	)
	if err != nil {
		t.Fatalf("list refunds: %v", err)
	}
	if len(page.Refunds) != 1 || page.NextCursor != "cursor_2" || requests != 2 {
		t.Fatalf("page=%+v requests=%d", page, requests)
	}
}

func TestRefundReadsRejectUnsafeIdentifiersAndPaginationBeforeNetwork(t *testing.T) {
	requests := 0
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		})}),
	)

	if _, err := client.Refunds.Get(context.Background(), "../refund"); err == nil {
		t.Fatal("unsafe refund id accepted")
	}
	if _, err := client.Refunds.ListByCharge(context.Background(), "charge_1", mupag.RefundListParams{Limit: 101}); err == nil {
		t.Fatal("oversized limit accepted")
	}
	if _, err := client.Refunds.ListByCharge(context.Background(), "charge_1", mupag.RefundListParams{Cursor: "bad cursor"}); err == nil {
		t.Fatal("unsafe cursor accepted")
	}
	if requests != 0 {
		t.Fatalf("network requests = %d, want 0", requests)
	}
}

func int64Pointer(value int64) *int64 { return &value }
