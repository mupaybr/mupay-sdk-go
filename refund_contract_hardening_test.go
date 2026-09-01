package mupag_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	mupag "github.com/mupaybr/mupag-sdk-go"
)

func TestRefundResponsesRequireRequestedAt(t *testing.T) {
	t.Run("create missing requested_at is outcome unknown", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writeJSON(writer, http.StatusAccepted, `{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":100,"status":"requested"}`)
		}))
		defer server.Close()

		amount := int64(100)
		refund, err := newTestClient(server.URL).Refunds.Create(
			context.Background(),
			"charge_1",
			mupag.RefundCreateParams{AmountCents: &amount},
			mupag.WithIdempotencyKey("refund-requested-at"),
		)

		var outcomeErr *mupag.OutcomeUnknownError
		if !errors.As(err, &outcomeErr) || refund != nil {
			t.Fatalf("refund = %#v, err = %T (%v); want outcome unknown", refund, err, err)
		}
	})

	for _, test := range []struct {
		name string
		body string
		call func(*mupag.Client) error
	}{
		{
			name: "get missing requested_at",
			body: `{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":100,"status":"completed"}`,
			call: func(client *mupag.Client) error {
				_, err := client.Refunds.Get(context.Background(), "refund_1")
				return err
			},
		},
		{
			name: "list missing requested_at",
			body: `{"refunds":[{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":100,"status":"completed"}]}`,
			call: func(client *mupag.Client) error {
				_, err := client.Refunds.ListByCharge(context.Background(), "charge_1", mupag.RefundListParams{})
				return err
			},
		},
		{
			name: "get invalid requested_at",
			body: `{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":100,"status":"completed","requested_at":"2026-02-30T12:00:00Z"}`,
			call: func(client *mupag.Client) error {
				_, err := client.Refunds.Get(context.Background(), "refund_1")
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writeJSON(writer, http.StatusOK, test.body)
			}))
			defer server.Close()

			if err := test.call(newTestClient(server.URL)); err == nil {
				t.Fatal("invalid refund response was accepted")
			}
		})
	}
}

func TestRefundCreateCorrelatesRequestedReason(t *testing.T) {
	amount := int64(100)
	for _, test := range []struct {
		name        string
		response    string
		wantUnknown bool
	}{
		{
			name:     "identical reason",
			response: `{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":100,"status":"requested","reason":"customer_request","requested_at":"2026-08-31T12:00:00Z"}`,
		},
		{
			name:        "missing reason",
			response:    `{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":100,"status":"requested","requested_at":"2026-08-31T12:00:00Z"}`,
			wantUnknown: true,
		},
		{
			name:        "divergent reason",
			response:    `{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":100,"status":"requested","reason":"duplicate","requested_at":"2026-08-31T12:00:00Z"}`,
			wantUnknown: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writeJSON(writer, http.StatusAccepted, test.response)
			}))
			defer server.Close()

			refund, err := newTestClient(server.URL).Refunds.Create(
				context.Background(),
				"charge_1",
				mupag.RefundCreateParams{AmountCents: &amount, Reason: "customer_request"},
				mupag.WithIdempotencyKey("refund-reason"),
			)
			if test.wantUnknown {
				var outcomeErr *mupag.OutcomeUnknownError
				if !errors.As(err, &outcomeErr) || refund != nil {
					t.Fatalf("refund = %#v, err = %T (%v); want outcome unknown", refund, err, err)
				}
				return
			}
			if err != nil || refund == nil || refund.Reason != "customer_request" {
				t.Fatalf("refund = %#v, err = %v", refund, err)
			}
		})
	}
}
