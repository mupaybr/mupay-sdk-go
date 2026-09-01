package mupag_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	mupag "github.com/mupaybr/mupag-sdk-go"
)

func TestChargeCreateCorrelatesResponseAmount(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		key         string
		couponCode  string
		wantAmount  int64
		wantUnknown bool
	}{
		{
			name:       "matching amount",
			body:       `{"charge_id":"charge_1","status":"pending","amount_cents":100}`,
			key:        "charge-correlation-match",
			wantAmount: 100,
		},
		{
			name:        "different amount",
			body:        `{"charge_id":"charge_1","status":"pending","amount_cents":200}`,
			key:         "charge-correlation-mismatch",
			wantUnknown: true,
		},
		{
			name:       "coupon discount and review status",
			body:       `{"charge_id":"charge_1","status":"under_review","amount_cents":50}`,
			key:        "charge-correlation-coupon",
			couponCode: "SAVE50",
			wantAmount: 50,
		},
		{
			name:        "coupon response exceeds requested amount",
			body:        `{"charge_id":"charge_1","status":"pending","amount_cents":200}`,
			key:         "charge-correlation-coupon-increase",
			couponCode:  "SAVE50",
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

			params := validPixCharge()
			params.CouponCode = test.couponCode
			charge, err := client.Charges.Create(
				context.Background(),
				params,
				mupag.WithIdempotencyKey(test.key),
			)
			assertCorrelationOutcome(t, err, test.key, test.wantUnknown)
			if test.wantUnknown && charge != nil {
				t.Fatalf("charge = %#v, want nil for uncorrelated response", charge)
			}
			if !test.wantUnknown && (charge == nil || charge.AmountCents != test.wantAmount) {
				t.Fatalf("charge = %#v, want matching response", charge)
			}
			if attempts != 1 {
				t.Fatalf("attempts = %d, want 1", attempts)
			}
		})
	}
}

func TestChargeCreateDoesNotConfirmCouponDiscountAfterAmbiguousRetry(t *testing.T) {
	var attempts int
	var sentKeys []string
	client := testClientWithResults(
		t,
		1,
		[]roundTripResult{
			{response: jsonHTTPResponse(http.StatusServiceUnavailable, `{"code":"temporarily_unavailable"}`)},
			{response: jsonHTTPResponse(http.StatusCreated, `{"charge_id":"charge_1","status":"under_review","amount_cents":50}`)},
		},
		&attempts,
		&sentKeys,
	)
	params := validPixCharge()
	params.CouponCode = "SAVE50"

	charge, err := client.Charges.Create(
		context.Background(),
		params,
		mupag.WithIdempotencyKey("coupon-ambiguous-retry-key"),
	)

	var outcomeErr *mupag.OutcomeUnknownError
	if !errors.As(err, &outcomeErr) {
		t.Fatalf("err = %T (%v), want *mupag.OutcomeUnknownError", err, err)
	}
	if charge != nil {
		t.Fatalf("charge = %#v, want nil", charge)
	}
	if outcomeErr.IdempotencyKey != "coupon-ambiguous-retry-key" {
		t.Fatalf("idempotency key = %q", outcomeErr.IdempotencyKey)
	}
	if attempts != 2 || len(sentKeys) != 2 || sentKeys[0] != sentKeys[1] {
		t.Fatalf("attempts = %d, keys = %#v", attempts, sentKeys)
	}
}

func TestChargeCreateDoesNotConfirmDivergentCouponAfterAmbiguousRetry(t *testing.T) {
	var attempts int
	var sentKeys []string
	client := testClientWithResults(
		t,
		1,
		[]roundTripResult{
			{response: jsonHTTPResponse(http.StatusServiceUnavailable, `{"code":"temporarily_unavailable"}`)},
			{response: jsonHTTPResponse(http.StatusCreated, `{"charge_id":"charge_1","status":"under_review","amount_cents":100,"coupon_code":"OTHER"}`)},
		},
		&attempts,
		&sentKeys,
	)
	params := validPixCharge()
	params.CouponCode = "SAVE50"

	charge, err := client.Charges.Create(
		context.Background(),
		params,
		mupag.WithIdempotencyKey("coupon-divergent-retry-key"),
	)

	var outcomeErr *mupag.OutcomeUnknownError
	if !errors.As(err, &outcomeErr) {
		t.Fatalf("err = %T (%v), want *mupag.OutcomeUnknownError", err, err)
	}
	if charge != nil {
		t.Fatalf("charge = %#v, want nil", charge)
	}
	if outcomeErr.IdempotencyKey != "coupon-divergent-retry-key" {
		t.Fatalf("idempotency key = %q", outcomeErr.IdempotencyKey)
	}
	if attempts != 2 || len(sentKeys) != 2 || sentKeys[0] != sentKeys[1] {
		t.Fatalf("attempts = %d, keys = %#v", attempts, sentKeys)
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
			body:     `{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":100,"status":"requested","requested_at":"2026-08-31T12:00:00Z"}`,
			key:      "refund-partial-match",
		},
		{
			name:        "partial refund has different charge",
			chargeID:    "charge_1",
			params:      mupag.RefundCreateParams{AmountCents: &partialAmount},
			body:        `{"refund_id":"refund_1","charge_id":"charge_2","amount_cents":100,"status":"requested","requested_at":"2026-08-31T12:00:00Z"}`,
			key:         "refund-partial-charge-mismatch",
			wantUnknown: true,
		},
		{
			name:        "partial refund has different amount",
			chargeID:    "charge_1",
			params:      mupag.RefundCreateParams{AmountCents: &partialAmount},
			body:        `{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":200,"status":"requested","requested_at":"2026-08-31T12:00:00Z"}`,
			key:         "refund-partial-amount-mismatch",
			wantUnknown: true,
		},
		{
			name:     "full refund does not invent amount correlation",
			chargeID: "charge_1",
			params:   mupag.RefundCreateParams{Full: true},
			body:     `{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":275,"status":"requested","requested_at":"2026-08-31T12:00:00Z"}`,
			key:      "refund-full-match",
		},
		{
			name:        "full refund has different charge",
			chargeID:    "charge_1",
			params:      mupag.RefundCreateParams{Full: true},
			body:        `{"refund_id":"refund_1","charge_id":"charge_2","amount_cents":275,"status":"requested","requested_at":"2026-08-31T12:00:00Z"}`,
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

func TestFullRefundWithoutModeEchoDoesNotConfirmAfterAmbiguousRetry(t *testing.T) {
	var attempts int
	client := testClientWithResults(
		t,
		1,
		[]roundTripResult{
			{response: jsonHTTPResponse(http.StatusServiceUnavailable, `{"code":"temporarily_unavailable"}`)},
			{response: jsonHTTPResponse(http.StatusAccepted, `{"refund_id":"refund_1","charge_id":"charge_1","amount_cents":275,"status":"requested","requested_at":"2026-08-31T12:00:00Z"}`)},
		},
		&attempts,
		nil,
	)

	refund, err := client.Refunds.Create(
		context.Background(),
		"charge_1",
		mupag.RefundCreateParams{Full: true},
		mupag.WithIdempotencyKey("refund-full-ambiguous-key"),
	)

	assertCorrelationOutcome(t, err, "refund-full-ambiguous-key", true)
	if refund != nil {
		t.Fatalf("refund = %#v, want nil", refund)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
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
			body: `{"id":"subscription_1","status":"canceled","cancel_at_period_end":false}`,
			key:  "subscription-cancel-match",
		},
		{
			name:        "different subscription",
			body:        `{"id":"subscription_2","status":"canceled","cancel_at_period_end":false}`,
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

func TestSubscriptionCancelAcceptsEveryModeCompatibleResponse(t *testing.T) {
	tests := []struct {
		name              string
		mode              string
		status            string
		cancelAtPeriodEnd bool
	}{
		{name: "immediate final cancellation", mode: "immediate", status: "canceled"},
		{name: "scheduled from trialing", mode: "end_of_period", status: "trialing", cancelAtPeriodEnd: true},
		{name: "scheduled from active", mode: "end_of_period", status: "active", cancelAtPeriodEnd: true},
		{name: "scheduled from past due", mode: "end_of_period", status: "past_due", cancelAtPeriodEnd: true},
		{name: "scheduled from unpaid", mode: "end_of_period", status: "unpaid", cancelAtPeriodEnd: true},
		{name: "scheduled from paused", mode: "end_of_period", status: "paused", cancelAtPeriodEnd: true},
		{name: "scheduled from incomplete", mode: "end_of_period", status: "incomplete", cancelAtPeriodEnd: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var attempts int
			body := fmt.Sprintf(
				`{"id":"subscription_1","status":%q,"cancel_at_period_end":%t}`,
				test.status,
				test.cancelAtPeriodEnd,
			)
			client := testClientWithResults(
				t,
				0,
				[]roundTripResult{{response: jsonHTTPResponse(http.StatusOK, body)}},
				&attempts,
				nil,
			)

			subscription, err := client.Subscriptions.Cancel(
				context.Background(),
				"subscription_1",
				mupag.CancelSubscriptionParams{Mode: test.mode},
				mupag.WithIdempotencyKey("subscription-mode-compatible"),
			)
			if err != nil {
				t.Fatalf("compatible response returned error: %v", err)
			}
			if subscription == nil || subscription.Status != test.status || subscription.CancelAtPeriodEnd != test.cancelAtPeriodEnd {
				t.Fatalf("subscription = %#v, want status=%q cancel_at_period_end=%t", subscription, test.status, test.cancelAtPeriodEnd)
			}
			if attempts != 1 {
				t.Fatalf("attempts = %d, want 1", attempts)
			}
		})
	}
}

func TestSubscriptionCancelTreatsModeIncompatibleResponseAsOutcomeUnknown(t *testing.T) {
	tests := []struct {
		name string
		mode string
		body string
	}{
		{name: "immediate non-final status", mode: "immediate", body: `{"id":"subscription_1","status":"active","cancel_at_period_end":false}`},
		{name: "immediate scheduled marker", mode: "immediate", body: `{"id":"subscription_1","status":"canceled","cancel_at_period_end":true}`},
		{name: "immediate missing marker", mode: "immediate", body: `{"id":"subscription_1","status":"canceled"}`},
		{name: "scheduled terminal status", mode: "end_of_period", body: `{"id":"subscription_1","status":"canceled","cancel_at_period_end":true}`},
		{name: "scheduled unsupported status", mode: "end_of_period", body: `{"id":"subscription_1","status":"mystery","cancel_at_period_end":true}`},
		{name: "scheduled false marker", mode: "end_of_period", body: `{"id":"subscription_1","status":"active","cancel_at_period_end":false}`},
		{name: "scheduled missing marker", mode: "end_of_period", body: `{"id":"subscription_1","status":"active"}`},
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
			key := "subscription-mode-contract"

			subscription, err := client.Subscriptions.Cancel(
				context.Background(),
				"subscription_1",
				mupag.CancelSubscriptionParams{Mode: test.mode},
				mupag.WithIdempotencyKey(key),
			)
			assertCorrelationOutcome(t, err, key, true)
			if subscription != nil {
				t.Fatalf("subscription = %#v, want nil for mode-incompatible response", subscription)
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
