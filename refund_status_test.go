package mupag_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	mupag "github.com/mupaybr/mupag-sdk-go"
)

func TestRefundsGetAcceptsEverySupportedStatus(t *testing.T) {
	for _, status := range []string{
		"requested",
		"processing",
		"completed",
		"failed",
		"cancelled",
		"unknown",
	} {
		t.Run(status, func(t *testing.T) {
			var attempts int
			client := testClientWithResults(
				t,
				0,
				[]roundTripResult{{response: jsonHTTPResponse(http.StatusOK, refundStatusBody(status))}},
				&attempts,
				nil,
			)

			refund, err := client.Refunds.Get(context.Background(), "refund_1")
			if err != nil || refund == nil || refund.Status != status {
				t.Fatalf("refund = %#v, err = %v", refund, err)
			}
			if attempts != 1 {
				t.Fatalf("attempts = %d, want 1", attempts)
			}
		})
	}
}

func TestRefundsCreateRejectsUnknownStatusAsOutcomeUnknown(t *testing.T) {
	var attempts int
	client := testClientWithResults(
		t,
		0,
		[]roundTripResult{{response: jsonHTTPResponse(http.StatusAccepted, refundStatusBody("pending"))}},
		&attempts,
		nil,
	)
	amount := int64(100)

	refund, err := client.Refunds.Create(
		context.Background(),
		"charge_1",
		mupag.RefundCreateParams{AmountCents: &amount},
		mupag.WithIdempotencyKey("refund-status-create"),
	)
	var outcomeErr *mupag.OutcomeUnknownError
	if !errors.As(err, &outcomeErr) {
		t.Fatalf("err = %T (%v), want *mupag.OutcomeUnknownError", err, err)
	}
	if outcomeErr.IdempotencyKey != "refund-status-create" {
		t.Fatalf("idempotency key = %q", outcomeErr.IdempotencyKey)
	}
	if refund != nil {
		t.Fatalf("refund = %#v, want nil", refund)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestRefundsGetRejectsUnknownStatusWithoutOutcomeUnknown(t *testing.T) {
	var attempts int
	client := testClientWithResults(
		t,
		0,
		[]roundTripResult{{response: jsonHTTPResponse(http.StatusOK, refundStatusBody("pending"))}},
		&attempts,
		nil,
	)

	refund, err := client.Refunds.Get(context.Background(), "refund_1")
	if err == nil {
		t.Fatalf("refund = %#v, want response validation error", refund)
	}
	var outcomeErr *mupag.OutcomeUnknownError
	if errors.As(err, &outcomeErr) {
		t.Fatalf("read error was reported as unknown outcome: %v", err)
	}
	if refund != nil {
		t.Fatalf("refund = %#v, want nil", refund)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestRefundsListByChargeRejectsUnknownStatusWithoutOutcomeUnknown(t *testing.T) {
	var attempts int
	client := testClientWithResults(
		t,
		0,
		[]roundTripResult{{response: jsonHTTPResponse(
			http.StatusOK,
			`{"refunds":[`+refundStatusBody("pending")+`]}`,
		)}},
		&attempts,
		nil,
	)

	page, err := client.Refunds.ListByCharge(context.Background(), "charge_1", mupag.RefundListParams{})
	if err == nil {
		t.Fatalf("page = %#v, want response validation error", page)
	}
	var outcomeErr *mupag.OutcomeUnknownError
	if errors.As(err, &outcomeErr) {
		t.Fatalf("read error was reported as unknown outcome: %v", err)
	}
	if page != nil {
		t.Fatalf("page = %#v, want nil", page)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func refundStatusBody(status string) string {
	return `{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":100,"status":"` + status + `","requested_at":"2026-08-31T12:00:00Z"}`
}
