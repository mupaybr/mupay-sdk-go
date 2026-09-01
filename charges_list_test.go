package mupag_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mupag "github.com/mupaybr/mupag-sdk-go"
)

func TestChargesListUsesBoundedPublicCursorContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/v1/charges" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		if request.URL.Query().Get("status") != "refunded" || request.URL.Query().Get("limit") != "25" || request.URL.Query().Get("cursor") != "cursor_1" {
			t.Fatalf("query = %v", request.URL.Query())
		}
		writeJSON(writer, http.StatusOK, `{"data":[{"charge_id":"charge_1","amount_cents":100,"status":"refunded","created_at":"2026-08-10T12:00:00Z"}],"next_cursor":"cursor_2"}`)
	}))
	defer server.Close()

	page, err := newTestClient(server.URL).Charges.List(context.Background(), mupag.ChargeListParams{
		Status: "refunded",
		Limit:  25,
		Cursor: "cursor_1",
	})
	if err != nil {
		t.Fatalf("list charges: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].Status != "refunded" || page.NextCursor != "cursor_2" {
		t.Fatalf("page = %+v", page)
	}
}

func TestChargesListRejectsInvalidFiltersBeforeNetwork(t *testing.T) {
	requests := 0
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return nil, errors.New("network must not be reached")
		})}),
	)
	now := time.Now()
	later := now.Add(time.Hour)

	for _, params := range []mupag.ChargeListParams{
		{Status: "DROP TABLE"},
		{Limit: 101},
		{Cursor: "bad cursor"},
		{Cursor: "A"},
		{Cursor: "YQ=="},
		{Cursor: "AB"},
		{CreatedAtFrom: &later, CreatedAtTo: &now},
	} {
		if _, err := client.Charges.List(context.Background(), params); err == nil {
			t.Fatalf("invalid filters accepted: %+v", params)
		}
	}
	if requests != 0 {
		t.Fatalf("network requests = %d, want 0", requests)
	}
}

func TestChargesListRejectsInvalidItemsAndResponseCursor(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "invalid charge id",
			body: `{"data":[{"charge_id":"charge_1","amount_cents":100,"status":"pending"},{"charge_id":"..","amount_cents":100,"status":"pending"}]}`,
		},
		{
			name: "invalid charge status",
			body: `{"data":[{"charge_id":"charge_1","amount_cents":100,"status":"pending"},{"charge_id":"charge_2","amount_cents":100,"status":"unknown"}]}`,
		},
		{
			name: "invalid charge amount",
			body: `{"data":[{"charge_id":"charge_1","amount_cents":100,"status":"pending"},{"charge_id":"charge_2","amount_cents":9000000000000001,"status":"pending"}]}`,
		},
		{
			name: "charge amount below minimum",
			body: `{"data":[{"charge_id":"charge_1","amount_cents":0,"status":"pending"}]}`,
		},
		{
			name: "unsafe next cursor",
			body: `{"data":[{"charge_id":"charge_1","amount_cents":100,"status":"pending"}],"next_cursor":"bad cursor"}`,
		},
		{
			name: "undecodable next cursor",
			body: `{"data":[{"charge_id":"charge_1","amount_cents":100,"status":"pending","created_at":"2026-08-31T12:00:00Z"}],"next_cursor":"A"}`,
		},
		{
			name: "padded next cursor",
			body: `{"data":[{"charge_id":"charge_1","amount_cents":100,"status":"pending","created_at":"2026-08-31T12:00:00Z"}],"next_cursor":"YQ=="}`,
		},
		{
			name: "non-canonical next cursor",
			body: `{"data":[{"charge_id":"charge_1","amount_cents":100,"status":"pending","created_at":"2026-08-31T12:00:00Z"}],"next_cursor":"AB"}`,
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

			if _, err := newTestClient(server.URL).Charges.List(context.Background(), mupag.ChargeListParams{}); err == nil {
				t.Fatal("invalid charge page was accepted")
			}
			if requests != 1 {
				t.Fatalf("network requests = %d, want 1", requests)
			}
		})
	}
}

func TestChargesListAcceptsOneCentItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, `{"data":[{"charge_id":"charge_1","amount_cents":1,"status":"paid","created_at":"2026-08-31T12:00:00Z"}]}`)
	}))
	defer server.Close()

	page, err := newTestClient(server.URL).Charges.List(context.Background(), mupag.ChargeListParams{})
	if err != nil || page == nil || len(page.Data) != 1 || page.Data[0].AmountCents != 1 {
		t.Fatalf("page = %#v, err = %v", page, err)
	}
}

func TestChargesListRejectsResponsesBeyondRequestedLimit(t *testing.T) {
	charge := `{"charge_id":"charge_1","amount_cents":1,"status":"paid","created_at":"2026-08-31T12:00:00Z"}`
	tests := []struct {
		name      string
		params    mupag.ChargeListParams
		itemCount int
		wantError bool
	}{
		{name: "default limit", itemCount: 26, wantError: true},
		{name: "requested limit", params: mupag.ChargeListParams{Limit: 2}, itemCount: 3, wantError: true},
		{name: "exact requested limit", params: mupag.ChargeListParams{Limit: 2}, itemCount: 2},
		{name: "public maximum", params: mupag.ChargeListParams{Limit: 100}, itemCount: 100},
		{name: "above public maximum", params: mupag.ChargeListParams{Limit: 100}, itemCount: 101, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := `{"data":[` + strings.TrimSuffix(strings.Repeat(charge+",", test.itemCount), ",") + `]}`
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writeJSON(writer, http.StatusOK, body)
			}))
			defer server.Close()

			page, err := newTestClient(server.URL).Charges.List(context.Background(), test.params)
			if test.wantError {
				if err == nil || page != nil {
					t.Fatalf("page = %#v, err = %v; want bounded-page error", page, err)
				}
				return
			}
			if err != nil || page == nil || len(page.Data) != test.itemCount {
				t.Fatalf("page = %#v, err = %v", page, err)
			}
		})
	}
}

func TestChargesListRequiresDataCollection(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantError bool
	}{
		{name: "missing data", body: `{}`, wantError: true},
		{name: "null data", body: `{"data":null}`, wantError: true},
		{name: "empty data", body: `{"data":[]}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writeJSON(writer, http.StatusOK, test.body)
			}))
			defer server.Close()

			page, err := newTestClient(server.URL).Charges.List(context.Background(), mupag.ChargeListParams{})
			if test.wantError {
				if err == nil {
					t.Fatal("charge page without a data collection was accepted")
				}
				return
			}
			if err != nil {
				t.Fatalf("list empty charge page: %v", err)
			}
			if page.Data == nil || len(page.Data) != 0 {
				t.Fatalf("data = %#v, want a non-nil empty collection", page.Data)
			}
		})
	}
}

func TestChargesListCorrelatesStatusFilter(t *testing.T) {
	tests := []struct {
		name            string
		requestedStatus string
		returnedStatus  string
		wantError       bool
	}{
		{name: "matching status", requestedStatus: "paid", returnedStatus: "paid"},
		{name: "matching review status", requestedStatus: "under_review", returnedStatus: "under_review"},
		{name: "different status", requestedStatus: "paid", returnedStatus: "refunded", wantError: true},
		{name: "no status filter", returnedStatus: "refunded"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests++
				if request.URL.Query().Get("status") != test.requestedStatus {
					t.Fatalf("status query = %q", request.URL.Query().Get("status"))
				}
				writeJSON(writer, http.StatusOK, `{"data":[{"charge_id":"charge_1","amount_cents":100,"status":"`+test.returnedStatus+`","created_at":"2026-08-31T12:00:00Z"}]}`)
			}))
			defer server.Close()

			page, err := newTestClient(server.URL).Charges.List(context.Background(), mupag.ChargeListParams{Status: test.requestedStatus})
			if test.wantError {
				if err == nil {
					t.Fatalf("page = %#v, want status correlation error", page)
				}
				var outcomeErr *mupag.OutcomeUnknownError
				if errors.As(err, &outcomeErr) {
					t.Fatalf("read error was reported as unknown outcome: %v", err)
				}
				if page != nil {
					t.Fatalf("page = %#v, want nil", page)
				}
			} else if err != nil || page == nil || len(page.Data) != 1 || page.Data[0].Status != test.returnedStatus {
				t.Fatalf("page = %#v, err = %v", page, err)
			}
			if requests != 1 {
				t.Fatalf("requests = %d, want 1", requests)
			}
		})
	}
}

func TestChargesListCorrelatesCreatedAtBounds(t *testing.T) {
	from := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 31, 13, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		params    mupag.ChargeListParams
		body      string
		wantError bool
	}{
		{
			name:   "inclusive lower bound",
			params: mupag.ChargeListParams{CreatedAtFrom: &from},
			body:   `{"data":[{"charge_id":"charge_1","amount_cents":100,"status":"paid","created_at":"2026-08-31T12:00:00Z"}]}`,
		},
		{
			name:      "before lower bound",
			params:    mupag.ChargeListParams{CreatedAtFrom: &from},
			body:      `{"data":[{"charge_id":"charge_1","amount_cents":100,"status":"paid","created_at":"2026-08-31T11:59:59Z"}]}`,
			wantError: true,
		},
		{
			name:   "before exclusive upper bound",
			params: mupag.ChargeListParams{CreatedAtTo: &to},
			body:   `{"data":[{"charge_id":"charge_1","amount_cents":100,"status":"paid","created_at":"2026-08-31T12:59:59Z"}]}`,
		},
		{
			name:      "at exclusive upper bound",
			params:    mupag.ChargeListParams{CreatedAtTo: &to},
			body:      `{"data":[{"charge_id":"charge_1","amount_cents":100,"status":"paid","created_at":"2026-08-31T13:00:00Z"}]}`,
			wantError: true,
		},
		{
			name:      "missing required created at",
			body:      `{"data":[{"charge_id":"charge_1","amount_cents":100,"status":"paid"}]}`,
			wantError: true,
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

			page, err := newTestClient(server.URL).Charges.List(context.Background(), test.params)
			if test.wantError {
				if err == nil {
					t.Fatalf("page = %#v, want created_at correlation error", page)
				}
				if page != nil {
					t.Fatalf("page = %#v, want nil", page)
				}
			} else if err != nil || page == nil || len(page.Data) != 1 {
				t.Fatalf("page = %#v, err = %v", page, err)
			}
			if requests != 1 {
				t.Fatalf("requests = %d, want 1", requests)
			}
		})
	}
}

func TestChargesListAllowsFiltersOmittedFromResponseSchema(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("customer_id") != "customer_1" || request.URL.Query().Get("payment_method") != "pix" {
			t.Fatalf("query = %v", request.URL.Query())
		}
		writeJSON(writer, http.StatusOK, `{"data":[{"charge_id":"charge_1","amount_cents":100,"status":"paid","created_at":"2026-08-31T12:00:00Z"}]}`)
	}))
	defer server.Close()

	page, err := newTestClient(server.URL).Charges.List(context.Background(), mupag.ChargeListParams{
		CustomerID:    "customer_1",
		PaymentMethod: "pix",
	})
	if err != nil || page == nil || len(page.Data) != 1 {
		t.Fatalf("page = %#v, err = %v", page, err)
	}
}

func TestChargesListRejectsExplicitlyInvalidPaymentMethodForFilter(t *testing.T) {
	for _, test := range []struct {
		name          string
		paymentMethod string
	}{
		{name: "divergent", paymentMethod: `"credit_card"`},
		{name: "null", paymentMethod: `null`},
		{name: "empty", paymentMethod: `""`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Query().Get("payment_method") != "pix" {
					t.Fatalf("query = %v", request.URL.Query())
				}
				body := fmt.Sprintf(`{"data":[{"charge_id":"charge_1","amount_cents":100,"status":"paid","payment_method":%s,"created_at":"2026-08-31T12:00:00Z"}]}`, test.paymentMethod)
				writeJSON(writer, http.StatusOK, body)
			}))
			defer server.Close()

			page, err := newTestClient(server.URL).Charges.List(context.Background(), mupag.ChargeListParams{PaymentMethod: "pix"})
			if err == nil || page != nil {
				t.Fatalf("page = %#v, err = %v, want payment method filter error", page, err)
			}
		})
	}
}

func TestChargesListReturnsMatchingPaymentMethod(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, `{"data":[{"charge_id":"charge_1","amount_cents":100,"status":"paid","payment_method":"pix","created_at":"2026-08-31T12:00:00Z"}]}`)
	}))
	defer server.Close()

	page, err := newTestClient(server.URL).Charges.List(context.Background(), mupag.ChargeListParams{PaymentMethod: "pix"})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if page == nil || len(page.Data) != 1 || page.Data[0].PaymentMethod != "pix" {
		t.Fatalf("page = %#v, want matching public payment method", page)
	}
}

func TestChargesListRejectsDivergentCustomerEchoForFilter(t *testing.T) {
	for _, test := range []struct {
		name string
		echo string
	}{
		{name: "customer_id", echo: `"customer_id":"customer_2"`},
		{name: "customer.id", echo: `"customer":{"id":"customer_2"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Query().Get("customer_id") != "customer_1" {
					t.Fatalf("query = %v", request.URL.Query())
				}
				body := fmt.Sprintf(`{"data":[{"charge_id":"charge_1","amount_cents":100,"status":"paid","created_at":"2026-08-31T12:00:00Z",%s}]}`, test.echo)
				writeJSON(writer, http.StatusOK, body)
			}))
			defer server.Close()

			page, err := newTestClient(server.URL).Charges.List(context.Background(), mupag.ChargeListParams{CustomerID: "customer_1"})
			if err == nil || page != nil {
				t.Fatalf("page = %#v, err = %v, want customer filter error", page, err)
			}
		})
	}
}
