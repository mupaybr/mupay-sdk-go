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
		{
			name:        "explicitly divergent coupon",
			body:        `{"charge_id":"charge_1","status":"pending","amount_cents":100,"coupon_code":"OTHER"}`,
			key:         "charge-correlation-coupon-divergent",
			couponCode:  "SAVE50",
			wantUnknown: true,
		},
		{
			name:       "null coupon without requested coupon",
			body:       `{"charge_id":"charge_1","status":"pending","amount_cents":100,"coupon_code":null}`,
			key:        "charge-correlation-null-coupon-absent",
			wantAmount: 100,
		},
		{
			name:        "null coupon with requested coupon",
			body:        `{"charge_id":"charge_1","status":"pending","amount_cents":100,"coupon_code":null}`,
			key:         "charge-correlation-null-coupon-divergent",
			couponCode:  "SAVE50",
			wantUnknown: true,
		},
		{
			name:        "divergent payment method echo",
			body:        `{"charge_id":"charge_1","status":"pending","amount_cents":100,"payment_method":"credit_card"}`,
			key:         "charge-correlation-payment-method-divergent",
			wantUnknown: true,
		},
		{
			name:        "null payment method echo",
			body:        `{"charge_id":"charge_1","status":"pending","amount_cents":100,"payment_method":null}`,
			key:         "charge-correlation-payment-method-null",
			wantUnknown: true,
		},
		{
			name:        "invalid optional psp charge id type",
			body:        `{"charge_id":"charge_1","status":"pending","amount_cents":100,"psp_charge_id":42}`,
			key:         "charge-correlation-psp-charge-id-type",
			wantUnknown: true,
		},
		{
			name:        "invalid optional expiration timestamp",
			body:        `{"charge_id":"charge_1","status":"pending","amount_cents":100,"expires_at":"not-an-instant"}`,
			key:         "charge-correlation-expiration-format",
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

func TestChargeCreateReturnsValidatedPaymentMethodEcho(t *testing.T) {
	var attempts int
	client := testClientWithResults(
		t,
		0,
		[]roundTripResult{{response: jsonHTTPResponse(http.StatusCreated, `{"charge_id":"charge_1","status":"pending","amount_cents":100,"payment_method":"pix"}`)}},
		&attempts,
		nil,
	)

	charge, err := client.Charges.Create(context.Background(), validPixCharge())
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if charge == nil || charge.PaymentMethod != "pix" {
		t.Fatalf("charge = %#v, want payment_method pix", charge)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestChargeCreateCorrelatesCustomerEcho(t *testing.T) {
	for _, test := range []struct {
		name        string
		echo        string
		wantUnknown bool
	}{
		{name: "matching customer_id", echo: `"customer_id":"cus_123"`},
		{name: "matching customer.id", echo: `"customer":{"id":"cus_123"}`},
		{name: "divergent customer_id", echo: `"customer_id":"cus_other"`, wantUnknown: true},
		{name: "divergent customer.id", echo: `"customer":{"id":"cus_other"}`, wantUnknown: true},
		{name: "null customer_id", echo: `"customer_id":null`, wantUnknown: true},
		{name: "null customer.id", echo: `"customer":{"id":null}`, wantUnknown: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var attempts int
			body := fmt.Sprintf(`{"charge_id":"charge_1","status":"pending","amount_cents":100,%s}`, test.echo)
			client := testClientWithResults(
				t,
				0,
				[]roundTripResult{{response: jsonHTTPResponse(http.StatusCreated, body)}},
				&attempts,
				nil,
			)
			key := "charge-customer-echo"

			charge, err := client.Charges.Create(
				context.Background(),
				validPixCharge(),
				mupag.WithIdempotencyKey(key),
			)
			assertCorrelationOutcome(t, err, key, test.wantUnknown)
			if test.wantUnknown && charge != nil {
				t.Fatalf("charge = %#v, want nil for uncorrelated customer", charge)
			}
			if !test.wantUnknown && charge == nil {
				t.Fatal("matching customer echo returned nil charge")
			}
			if attempts != 1 {
				t.Fatalf("attempts = %d, want 1", attempts)
			}
		})
	}
}

func TestChargeCreateValidatesGeneratedCustomerAliases(t *testing.T) {
	for _, test := range []struct {
		name        string
		echo        string
		wantUnknown bool
	}{
		{name: "aliases omitted"},
		{name: "valid customer_id", echo: `,"customer_id":"cus_created"`},
		{name: "valid customer.id", echo: `,"customer":{"id":"cus_created"}`},
		{name: "matching aliases", echo: `,"customer_id":"cus_created","customer":{"id":"cus_created"}`},
		{name: "conflicting aliases", echo: `,"customer_id":"cus_a","customer":{"id":"cus_b"}`, wantUnknown: true},
		{name: "null customer_id", echo: `,"customer_id":null`, wantUnknown: true},
		{name: "numeric customer_id", echo: `,"customer_id":42`, wantUnknown: true},
		{name: "invalid customer_id", echo: `,"customer_id":".."`, wantUnknown: true},
		{name: "null customer", echo: `,"customer":null`, wantUnknown: true},
		{name: "customer without id", echo: `,"customer":{"name":"Ana"}`, wantUnknown: true},
		{name: "null customer.id", echo: `,"customer":{"id":null}`, wantUnknown: true},
		{name: "numeric customer.id", echo: `,"customer":{"id":42}`, wantUnknown: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var attempts int
			body := fmt.Sprintf(`{"charge_id":"charge_1","status":"pending","amount_cents":100%s}`, test.echo)
			client := testClientWithResults(
				t,
				0,
				[]roundTripResult{{response: jsonHTTPResponse(http.StatusCreated, body)}},
				&attempts,
				nil,
			)
			params := validPixCharge()
			params.Customer.ID = ""
			key := "charge-generated-customer-echo"

			charge, err := client.Charges.Create(
				context.Background(),
				params,
				mupag.WithIdempotencyKey(key),
			)
			assertCorrelationOutcome(t, err, key, test.wantUnknown)
			if test.wantUnknown && charge != nil {
				t.Fatalf("charge = %#v, want nil for invalid generated customer echo", charge)
			}
			if !test.wantUnknown && charge == nil {
				t.Fatal("valid generated customer echo returned nil charge")
			}
			if attempts != 1 {
				t.Fatalf("attempts = %d, want 1", attempts)
			}
		})
	}
}

func TestChargeCreateCorrelatesExternalReferenceEcho(t *testing.T) {
	for _, test := range []struct {
		name                       string
		echo                       string
		requestedExternalReference string
		wantUnknown                bool
	}{
		{name: "requested matching", echo: `,"external_reference":"order_123"`, requestedExternalReference: "order_123"},
		{name: "requested echo omitted", requestedExternalReference: "order_123"},
		{name: "requested divergent", echo: `,"external_reference":"order_other"`, requestedExternalReference: "order_123", wantUnknown: true},
		{name: "requested null", echo: `,"external_reference":null`, requestedExternalReference: "order_123", wantUnknown: true},
		{name: "unrequested echo omitted"},
		{name: "unrequested null", echo: `,"external_reference":null`},
		{name: "unrequested non-null", echo: `,"external_reference":"order_other"`, wantUnknown: true},
		{name: "unrequested invalid type", echo: `,"external_reference":42`, wantUnknown: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var attempts int
			body := fmt.Sprintf(`{"charge_id":"charge_1","status":"pending","amount_cents":100%s}`, test.echo)
			client := testClientWithResults(
				t,
				0,
				[]roundTripResult{{response: jsonHTTPResponse(http.StatusCreated, body)}},
				&attempts,
				nil,
			)
			params := validPixCharge()
			params.ExternalReference = test.requestedExternalReference
			key := "charge-external-reference-echo"

			charge, err := client.Charges.Create(
				context.Background(),
				params,
				mupag.WithIdempotencyKey(key),
			)
			assertCorrelationOutcome(t, err, key, test.wantUnknown)
			if test.wantUnknown && charge != nil {
				t.Fatalf("charge = %#v, want nil for uncorrelated external reference", charge)
			}
			if !test.wantUnknown && charge == nil {
				t.Fatal("compatible external reference echo returned nil charge")
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

func TestChargeCreateDoesNotConfirmDivergentEchoAfterAmbiguousRetry(t *testing.T) {
	tests := []struct {
		name              string
		body              string
		key               string
		externalReference string
		withoutCustomerID bool
	}{
		{
			name: "divergent coupon",
			body: `{"charge_id":"charge_1","status":"under_review","amount_cents":100,"coupon_code":"OTHER"}`,
			key:  "coupon-divergent-retry-key",
		},
		{
			name: "null coupon",
			body: `{"charge_id":"charge_1","status":"under_review","amount_cents":100,"coupon_code":null}`,
			key:  "coupon-null-retry-key",
		},
		{
			name: "divergent payment method",
			body: `{"charge_id":"charge_1","status":"under_review","amount_cents":100,"payment_method":"credit_card"}`,
			key:  "payment-method-divergent-retry-key",
		},
		{
			name: "null payment method",
			body: `{"charge_id":"charge_1","status":"under_review","amount_cents":100,"payment_method":null}`,
			key:  "payment-method-null-retry-key",
		},
		{
			name: "divergent customer_id",
			body: `{"charge_id":"charge_1","status":"under_review","amount_cents":100,"customer_id":"cus_other"}`,
			key:  "customer-id-divergent-retry-key",
		},
		{
			name: "divergent customer.id",
			body: `{"charge_id":"charge_1","status":"under_review","amount_cents":100,"customer":{"id":"cus_other"}}`,
			key:  "nested-customer-id-divergent-retry-key",
		},
		{
			name:              "divergent external reference",
			body:              `{"charge_id":"charge_1","status":"under_review","amount_cents":100,"external_reference":"order_other"}`,
			key:               "external-reference-divergent-retry-key",
			externalReference: "order_123",
		},
		{
			name:              "conflicting generated customer aliases",
			body:              `{"charge_id":"charge_1","status":"under_review","amount_cents":100,"customer_id":"cus_a","customer":{"id":"cus_b"}}`,
			key:               "generated-customer-aliases-divergent-retry-key",
			withoutCustomerID: true,
		},
		{
			name: "unrequested external reference",
			body: `{"charge_id":"charge_1","status":"under_review","amount_cents":100,"external_reference":"order_other"}`,
			key:  "unrequested-external-reference-retry-key",
		},
		{
			name: "invalid optional psp charge id type",
			body: `{"charge_id":"charge_1","status":"under_review","amount_cents":100,"psp_charge_id":42}`,
			key:  "invalid-psp-charge-id-retry-key",
		},
		{
			name: "invalid optional expiration timestamp",
			body: `{"charge_id":"charge_1","status":"under_review","amount_cents":100,"expires_at":"not-an-instant"}`,
			key:  "invalid-expires-at-retry-key",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var attempts int
			var sentKeys []string
			client := testClientWithResults(
				t,
				1,
				[]roundTripResult{
					{response: jsonHTTPResponse(http.StatusServiceUnavailable, `{"code":"temporarily_unavailable"}`)},
					{response: jsonHTTPResponse(http.StatusCreated, test.body)},
				},
				&attempts,
				&sentKeys,
			)
			params := validPixCharge()
			params.CouponCode = "SAVE50"
			params.ExternalReference = test.externalReference
			if test.withoutCustomerID {
				params.Customer.ID = ""
			}

			charge, err := client.Charges.Create(
				context.Background(),
				params,
				mupag.WithIdempotencyKey(test.key),
			)

			var outcomeErr *mupag.OutcomeUnknownError
			if !errors.As(err, &outcomeErr) {
				t.Fatalf("err = %T (%v), want *mupag.OutcomeUnknownError", err, err)
			}
			if charge != nil {
				t.Fatalf("charge = %#v, want nil", charge)
			}
			if outcomeErr.IdempotencyKey != test.key {
				t.Fatalf("idempotency key = %q, want %q", outcomeErr.IdempotencyKey, test.key)
			}
			if attempts != 2 || len(sentKeys) != 2 || sentKeys[0] != sentKeys[1] {
				t.Fatalf("attempts = %d, keys = %#v", attempts, sentKeys)
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

func TestSubscriptionCancelCorrelatesCancellationReasonEcho(t *testing.T) {
	for _, test := range []struct {
		name           string
		reason         string
		echo           string
		afterAmbiguous bool
		wantUnknown    bool
	}{
		{name: "requested matching", reason: "customer_request", echo: `,"cancellation_reason":"customer_request"`},
		{name: "requested echo omitted", reason: "customer_request"},
		{name: "requested null", reason: "customer_request", echo: `,"cancellation_reason":null`, wantUnknown: true},
		{name: "requested divergent", reason: "customer_request", echo: `,"cancellation_reason":"other_reason"`, wantUnknown: true},
		{name: "requested invalid type", reason: "customer_request", echo: `,"cancellation_reason":42`, wantUnknown: true},
		{name: "unrequested echo omitted"},
		{name: "unrequested null", echo: `,"cancellation_reason":null`},
		{name: "unrequested non-null", echo: `,"cancellation_reason":"other_reason"`, wantUnknown: true},
		{name: "matching after ambiguity", reason: "customer_request", echo: `,"cancellation_reason":"customer_request"`, afterAmbiguous: true},
		{name: "divergent after ambiguity", reason: "customer_request", echo: `,"cancellation_reason":"other_reason"`, afterAmbiguous: true, wantUnknown: true},
		{name: "unrequested non-null after ambiguity", echo: `,"cancellation_reason":"other_reason"`, afterAmbiguous: true, wantUnknown: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var attempts int
			results := []roundTripResult{{response: jsonHTTPResponse(
				http.StatusOK,
				fmt.Sprintf(`{"id":"subscription_1","status":"canceled","cancel_at_period_end":false%s}`, test.echo),
			)}}
			maxRetries := 0
			if test.afterAmbiguous {
				maxRetries = 1
				results = append(
					[]roundTripResult{{response: jsonHTTPResponse(http.StatusServiceUnavailable, `{"code":"temporarily_unavailable"}`)}},
					results...,
				)
			}
			client := testClientWithResults(t, maxRetries, results, &attempts, nil)
			key := "subscription-cancellation-reason-echo"

			subscription, err := client.Subscriptions.Cancel(
				context.Background(),
				"subscription_1",
				mupag.CancelSubscriptionParams{Mode: "immediate", Reason: test.reason},
				mupag.WithIdempotencyKey(key),
			)
			assertCorrelationOutcome(t, err, key, test.wantUnknown)
			if test.wantUnknown && subscription != nil {
				t.Fatalf("subscription = %#v, want nil for uncorrelated cancellation reason", subscription)
			}
			if !test.wantUnknown && subscription == nil {
				t.Fatal("compatible cancellation reason returned nil subscription")
			}
			wantAttempts := 1
			if test.afterAmbiguous {
				wantAttempts = 2
			}
			if attempts != wantAttempts {
				t.Fatalf("attempts = %d, want %d", attempts, wantAttempts)
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
