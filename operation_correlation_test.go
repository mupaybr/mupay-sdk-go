package mupag_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	mupag "github.com/mupaybr/mupag-sdk-go"
)

func TestChargeCreateCorrelatesResponseAmount(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		key         string
		wantUnknown bool
	}{
		{
			name: "matching amount",
			body: `{"charge_id":"charge_1","status":"pending","amount_cents":100}`,
			key:  "charge-correlation-match",
		},
		{
			name:        "different amount",
			body:        `{"charge_id":"charge_1","status":"pending","amount_cents":200}`,
			key:         "charge-correlation-mismatch",
			wantUnknown: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var attempts int
			client := testClientWithResults(
				t,
				0,
				[]roundTripResult{{response: jsonHTTPResponse(http.StatusCreated, test.body)}},
				&attempts,
				nil,
			)

			charge, err := client.Charges.Create(
				context.Background(),
				validPixCharge(),
				mupag.WithIdempotencyKey(test.key),
			)
			assertCorrelationOutcome(t, err, test.key, test.wantUnknown)
			if test.wantUnknown && charge != nil {
				t.Fatalf("charge = %#v, want nil for uncorrelated response", charge)
			}
			if !test.wantUnknown && (charge == nil || charge.AmountCents != 100) {
				t.Fatalf("charge = %#v, want matching response", charge)
			}
			if attempts != 1 {
				t.Fatalf("attempts = %d, want 1", attempts)
			}
		})
	}
}

func TestRefundCreateCorrelatesKnownRequestFields(t *testing.T) {
	partialAmount := int64(100)
	tests := []struct {
		name        string
		chargeID    string
		params      mupag.RefundCreateParams
		body        string
		key         string
		wantUnknown bool
	}{
		{
			name:     "partial refund matches charge and amount",
			chargeID: "charge_1",
			params:   mupag.RefundCreateParams{AmountCents: &partialAmount},
			body:     `{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":100,"status":"requested"}`,
			key:      "refund-partial-match",
		},
		{
			name:        "partial refund has different charge",
			chargeID:    "charge_1",
			params:      mupag.RefundCreateParams{AmountCents: &partialAmount},
			body:        `{"refund_id":"refund_1","charge_id":"charge_2","amount_cents":100,"status":"requested"}`,
			key:         "refund-partial-charge-mismatch",
			wantUnknown: true,
		},
		{
			name:        "partial refund has different amount",
			chargeID:    "charge_1",
			params:      mupag.RefundCreateParams{AmountCents: &partialAmount},
			body:        `{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":200,"status":"requested"}`,
			key:         "refund-partial-amount-mismatch",
			wantUnknown: true,
		},
		{
			name:     "full refund does not invent amount correlation",
			chargeID: "charge_1",
			params:   mupag.RefundCreateParams{Full: true},
			body:     `{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":275,"status":"requested"}`,
			key:      "refund-full-match",
		},
		{
			name:        "full refund has different charge",
			chargeID:    "charge_1",
			params:      mupag.RefundCreateParams{Full: true},
			body:        `{"refund_id":"refund_1","charge_id":"charge_2","amount_cents":275,"status":"requested"}`,
			key:         "refund-full-charge-mismatch",
			wantUnknown: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var attempts int
			client := testClientWithResults(
				t,
				0,
				[]roundTripResult{{response: jsonHTTPResponse(http.StatusAccepted, test.body)}},
				&attempts,
				nil,
			)

			refund, err := client.Refunds.Create(
				context.Background(),
				test.chargeID,
				test.params,
				mupag.WithIdempotencyKey(test.key),
			)
			assertCorrelationOutcome(t, err, test.key, test.wantUnknown)
			if test.wantUnknown && refund != nil {
				t.Fatalf("refund = %#v, want nil for uncorrelated response", refund)
			}
			if !test.wantUnknown && (refund == nil || refund.ChargeID != test.chargeID) {
				t.Fatalf("refund = %#v, want matching response", refund)
			}
			if attempts != 1 {
				t.Fatalf("attempts = %d, want 1", attempts)
			}
		})
	}
}

func TestSubscriptionCancelCorrelatesResponseID(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		key         string
		wantUnknown bool
	}{
		{
			name: "matching subscription",
			body: `{"id":"subscription_1","status":"canceled"}`,
			key:  "subscription-cancel-match",
		},
		{
			name:        "different subscription",
			body:        `{"id":"subscription_2","status":"canceled"}`,
			key:         "subscription-cancel-mismatch",
			wantUnknown: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var attempts int
			client := testClientWithResults(
				t,
				0,
				[]roundTripResult{{response: jsonHTTPResponse(http.StatusOK, test.body)}},
				&attempts,
				nil,
			)

			subscription, err := client.Subscriptions.Cancel(
				context.Background(),
				"subscription_1",
				mupag.CancelSubscriptionParams{Mode: "immediate"},
				mupag.WithIdempotencyKey(test.key),
			)
			assertCorrelationOutcome(t, err, test.key, test.wantUnknown)
			if test.wantUnknown && subscription != nil {
				t.Fatalf("subscription = %#v, want nil for uncorrelated response", subscription)
			}
			if !test.wantUnknown && (subscription == nil || subscription.ID != "subscription_1") {
				t.Fatalf("subscription = %#v, want matching response", subscription)
			}
			if attempts != 1 {
				t.Fatalf("attempts = %d, want 1", attempts)
			}
		})
	}
}

func assertCorrelationOutcome(t *testing.T, err error, idempotencyKey string, wantUnknown bool) {
	t.Helper()
	var outcomeErr *mupag.OutcomeUnknownError
	if wantUnknown {
		if !errors.As(err, &outcomeErr) {
			t.Fatalf("err = %T (%v), want *mupag.OutcomeUnknownError", err, err)
		}
		if outcomeErr.IdempotencyKey != idempotencyKey {
			t.Fatalf("idempotency key = %q, want %q", outcomeErr.IdempotencyKey, idempotencyKey)
		}
		return
	}
	if err != nil {
		t.Fatalf("matching response returned error: %v", err)
	}
}
