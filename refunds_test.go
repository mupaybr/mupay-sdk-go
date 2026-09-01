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

func TestRefundOperationsRejectPANBeforeNetwork(t *testing.T) {
	requests := 0
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return nil, errors.New("network must not be reached")
		})}),
	)
	pan := "4111111111111111"

	operations := []struct {
		name string
		run  func() error
	}{
		{name: "create charge id", run: func() error {
			_, err := client.Refunds.Create(context.Background(), pan, mupag.RefundCreateParams{Full: true})
			return err
		}},
		{name: "create reason", run: func() error {
			_, err := client.Refunds.Create(context.Background(), "charge_1", mupag.RefundCreateParams{Full: true, Reason: "customer used 4111 1111 1111 1111"})
			return err
		}},
		{name: "create reason with control separators", run: func() error {
			_, err := client.Refunds.Create(context.Background(), "charge_1", mupag.RefundCreateParams{Full: true, Reason: "customer used 4111\x001111\x001111\x001111"})
			return err
		}},
		{name: "get refund id", run: func() error {
			_, err := client.Refunds.Get(context.Background(), pan)
			return err
		}},
		{name: "list charge id", run: func() error {
			_, err := client.Refunds.ListByCharge(context.Background(), pan, mupag.RefundListParams{})
			return err
		}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.run(); err == nil {
				t.Fatal("PAN-bearing refund operation was accepted")
			}
		})
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
		writeJSON(writer, http.StatusAccepted, `{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":100,"status":"requested","reason":"requested_by_customer","requested_at":"2026-08-31T12:00:00Z"}`)
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	refund, err := client.Refunds.Create(
		context.Background(),
		"charge_1",
		mupag.RefundCreateParams{Full: true, Reason: "  requested_by_customer\t"},
		mupag.WithIdempotencyKey("refund_order_1"),
	)
	if err != nil {
		t.Fatalf("create refund: %v", err)
	}
	if body["full"] != true || body["amount_cents"] != nil || idempotencyKey != "refund_order_1" {
		t.Fatalf("body=%v idempotency=%q", body, idempotencyKey)
	}
	if refund.RefundID != "refund_1" || refund.Status != "requested" {
		t.Fatalf("refund = %+v", refund)
	}
}

func TestRefundsCreateOmitsWhitespaceOnlyReason(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		writeJSON(writer, http.StatusAccepted, `{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":100,"status":"requested","requested_at":"2026-08-31T12:00:00Z"}`)
	}))
	defer server.Close()

	_, err := newTestClient(server.URL).Refunds.Create(
		context.Background(),
		"charge_1",
		mupag.RefundCreateParams{Full: true, Reason: " \t\r\n "},
		mupag.WithIdempotencyKey("refund_order_whitespace"),
	)
	if err != nil {
		t.Fatalf("create refund: %v", err)
	}
	if _, present := body["reason"]; present {
		t.Fatalf("whitespace-only reason was sent: %#v", body["reason"])
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
			writeJSON(writer, http.StatusOK, `{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":100,"status":"completed","requested_at":"2026-08-31T12:00:00Z"}`)
		case "/v1/charges/charge_1/refunds":
			if request.URL.Query().Get("limit") != "25" || request.URL.Query().Get("cursor") != "cursor_1" {
				t.Fatalf("query = %v", request.URL.Query())
			}
			writeJSON(writer, http.StatusOK, `{"refunds":[{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":100,"status":"completed","requested_at":"2026-08-31T12:00:00Z"}],"next_cursor":"cursor_2"}`)
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

func TestRefundsListByChargeTreatsEmptyNextCursorAsTerminal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(writer, http.StatusOK, `{"refunds":[],"next_cursor":""}`)
	}))
	defer server.Close()

	page, err := newTestClient(server.URL).Refunds.ListByCharge(context.Background(), "charge_1", mupag.RefundListParams{})
	if err != nil || page == nil || page.NextCursor != "" {
		t.Fatalf("page = %#v, err = %v, want terminal empty cursor", page, err)
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
	if _, err := client.Refunds.ListByCharge(context.Background(), "charge_1", mupag.RefundListParams{Cursor: "A"}); err == nil {
		t.Fatal("undecodable cursor accepted")
	}
	if _, err := client.Refunds.ListByCharge(context.Background(), "charge_1", mupag.RefundListParams{Cursor: "YQ=="}); err == nil {
		t.Fatal("padded cursor accepted")
	}
	if _, err := client.Refunds.ListByCharge(context.Background(), "charge_1", mupag.RefundListParams{Cursor: "AB"}); err == nil {
		t.Fatal("non-canonical cursor accepted")
	}
	if requests != 0 {
		t.Fatalf("network requests = %d, want 0", requests)
	}
}

func TestRefundsListByChargeRejectsInvalidItemsAndResponseCursor(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "invalid refund item",
			body: `{"refunds":[{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":100,"status":"completed","requested_at":"2026-08-31T12:00:00Z"},{"refund_id":"..","charge_id":"charge_1","amount_cents":100,"status":"completed","requested_at":"2026-08-31T12:00:00Z"}]}`,
		},
		{
			name: "unsafe next cursor",
			body: `{"refunds":[{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":100,"status":"completed","requested_at":"2026-08-31T12:00:00Z"}],"next_cursor":"bad cursor"}`,
		},
		{
			name: "undecodable next cursor",
			body: `{"refunds":[{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":100,"status":"completed","requested_at":"2026-08-31T12:00:00Z"}],"next_cursor":"A"}`,
		},
		{
			name: "padded next cursor",
			body: `{"refunds":[{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":100,"status":"completed","requested_at":"2026-08-31T12:00:00Z"}],"next_cursor":"YQ=="}`,
		},
		{
			name: "non-canonical next cursor",
			body: `{"refunds":[{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":100,"status":"completed","requested_at":"2026-08-31T12:00:00Z"}],"next_cursor":"AB"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				requests++
				writeJSON(writer, http.StatusOK, test.body)
			}))
			defer server.Close()

			if _, err := newTestClient(server.URL).Refunds.ListByCharge(
				context.Background(),
				"charge_1",
				mupag.RefundListParams{},
			); err == nil {
				t.Fatal("invalid refund page was accepted")
			}
			if requests != 1 {
				t.Fatalf("network requests = %d, want 1", requests)
			}
		})
	}
}

func TestRefundsGetCorrelatesRequestedID(t *testing.T) {
	tests := []struct {
		name             string
		responseRefundID string
		wantError        bool
	}{
		{name: "matching refund", responseRefundID: "refund_1"},
		{name: "different refund", responseRefundID: "refund_2", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writeJSON(writer, http.StatusOK, `{"refund_id":"`+test.responseRefundID+`","charge_id":"charge_1","amount_cents":100,"status":"completed","requested_at":"2026-08-31T12:00:00Z"}`)
			}))
			defer server.Close()

			refund, err := newTestClient(server.URL).Refunds.Get(context.Background(), "refund_1")
			if test.wantError {
				if err == nil {
					t.Fatalf("refund = %+v, want correlation error", refund)
				}
				return
			}
			if err != nil || refund.RefundID != "refund_1" {
				t.Fatalf("refund=%+v err=%v", refund, err)
			}
		})
	}
}

func TestRefundResponsesRejectOversizedReasons(t *testing.T) {
	oversizedReason := strings.Repeat("r", 501)
	tests := []struct {
		name      string
		path      string
		operation func(*mupag.Client) error
		body      string
	}{
		{
			name: "get",
			path: "/v1/refunds/refund_1",
			operation: func(client *mupag.Client) error {
				_, err := client.Refunds.Get(context.Background(), "refund_1")
				return err
			},
			body: `{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":100,"status":"completed","reason":"` + oversizedReason + `","requested_at":"2026-08-31T12:00:00Z"}`,
		},
		{
			name: "list",
			path: "/v1/charges/charge_1/refunds",
			operation: func(client *mupag.Client) error {
				_, err := client.Refunds.ListByCharge(context.Background(), "charge_1", mupag.RefundListParams{})
				return err
			},
			body: `{"refunds":[{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":100,"status":"completed","reason":"` + oversizedReason + `","requested_at":"2026-08-31T12:00:00Z"}]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != test.path {
					t.Fatalf("path = %s, want %s", request.URL.Path, test.path)
				}
				writeJSON(writer, http.StatusOK, test.body)
			}))
			defer server.Close()

			if err := test.operation(newTestClient(server.URL)); err == nil {
				t.Fatal("oversized refund reason was accepted")
			}
		})
	}
}

func TestRefundsListByChargeCorrelatesItems(t *testing.T) {
	tests := []struct {
		name             string
		responseChargeID string
		wantError        bool
	}{
		{name: "matching charge", responseChargeID: "charge_1"},
		{name: "different charge", responseChargeID: "charge_2", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writeJSON(writer, http.StatusOK, `{"refunds":[{"refund_id":"refund_1","charge_id":"`+test.responseChargeID+`","amount_cents":100,"status":"completed","requested_at":"2026-08-31T12:00:00Z"}]}`)
			}))
			defer server.Close()

			page, err := newTestClient(server.URL).Refunds.ListByCharge(
				context.Background(),
				"charge_1",
				mupag.RefundListParams{},
			)
			if test.wantError {
				if err == nil {
					t.Fatalf("page = %+v, want correlation error", page)
				}
				return
			}
			if err != nil || len(page.Refunds) != 1 || page.Refunds[0].ChargeID != "charge_1" {
				t.Fatalf("page=%+v err=%v", page, err)
			}
		})
	}
}

func TestRefundsListByChargeRejectsResponsesBeyondRequestedLimit(t *testing.T) {
	refund := `{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":1,"status":"completed","requested_at":"2026-08-31T12:00:00Z"}`
	tests := []struct {
		name      string
		params    mupag.RefundListParams
		itemCount int
		wantError bool
	}{
		{name: "default limit", itemCount: 100},
		{name: "requested limit", params: mupag.RefundListParams{Limit: 2}, itemCount: 3, wantError: true},
		{name: "exact requested limit", params: mupag.RefundListParams{Limit: 2}, itemCount: 2},
		{name: "public maximum", params: mupag.RefundListParams{Limit: 100}, itemCount: 100},
		{name: "above public maximum", params: mupag.RefundListParams{Limit: 100}, itemCount: 101, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := `{"refunds":[` + strings.TrimSuffix(strings.Repeat(refund+",", test.itemCount), ",") + `]}`
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writeJSON(writer, http.StatusOK, body)
			}))
			defer server.Close()

			page, err := newTestClient(server.URL).Refunds.ListByCharge(
				context.Background(),
				"charge_1",
				test.params,
			)
			if test.wantError {
				if err == nil || page != nil {
					t.Fatalf("page = %#v, err = %v; want bounded-page error", page, err)
				}
				return
			}
			if err != nil || page == nil || len(page.Refunds) != test.itemCount {
				t.Fatalf("page = %#v, err = %v", page, err)
			}
		})
	}
}

func TestRefundsListByChargeRequiresRefundCollection(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantError bool
	}{
		{name: "missing refunds", body: `{}`, wantError: true},
		{name: "null refunds", body: `{"refunds":null}`, wantError: true},
		{name: "empty refunds", body: `{"refunds":[]}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writeJSON(writer, http.StatusOK, test.body)
			}))
			defer server.Close()

			page, err := newTestClient(server.URL).Refunds.ListByCharge(
				context.Background(),
				"charge_1",
				mupag.RefundListParams{},
			)
			if test.wantError {
				if err == nil {
					t.Fatal("refund page without a refunds collection was accepted")
				}
				return
			}
			if err != nil {
				t.Fatalf("list empty refund page: %v", err)
			}
			if page.Refunds == nil || len(page.Refunds) != 0 {
				t.Fatalf("refunds = %#v, want a non-nil empty collection", page.Refunds)
			}
		})
	}
}

func int64Pointer(value int64) *int64 { return &value }
