package mupag_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	mupag "github.com/mupaybr/mupag-sdk-go"
)

type changingMetadataValue struct {
	calls int
}

func (value *changingMetadataValue) MarshalJSON() ([]byte, error) {
	value.calls++
	if value.calls == 1 {
		return []byte(`"validated"`), nil
	}
	return []byte(`"4111111111111111"`), nil
}

func TestClientFailsClosedWhenEnvironmentIsMissingOrKeyDoesNotMatch(t *testing.T) {
	requests := 0
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New("network must not be reached")
	})

	missingEnvironment := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithHTTPClient(&http.Client{Transport: transport}),
	)
	_, err := missingEnvironment.Charges.Create(context.Background(), mupag.ChargeCreateParams{})
	if err == nil || !strings.Contains(err.Error(), "environment") {
		t.Fatalf("missing environment error = %v", err)
	}

	mismatch := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithPrdEnvironment(),
		mupag.WithHTTPClient(&http.Client{Transport: transport}),
	)
	_, err = mismatch.Charges.Create(context.Background(), mupag.ChargeCreateParams{})
	if err == nil || !strings.Contains(err.Error(), "environment") {
		t.Fatalf("environment mismatch error = %v", err)
	}
	if requests != 0 {
		t.Fatalf("network requests = %d, want 0", requests)
	}
}

func TestClientRejectsCrossOriginBaseURLAndUnsafeAPIKeyBeforeNetwork(t *testing.T) {
	requests := 0
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New("network must not be reached")
	})
	tests := []struct {
		name    string
		options []mupag.Option
	}{
		{
			name: "test key to arbitrary https origin",
			options: []mupag.Option{
				mupag.WithAPIKey("sk_test_123"),
				mupag.WithTestEnvironment(),
				mupag.WithBaseURL("https://attacker.example"),
			},
		},
		{
			name: "prd key to loopback",
			options: []mupag.Option{
				mupag.WithAPIKey("sk_prd_123"),
				mupag.WithPrdEnvironment(),
				mupag.WithBaseURL("http://127.0.0.1:8080"),
			},
		},
		{
			name: "prd key to sandbox origin",
			options: []mupag.Option{
				mupag.WithAPIKey("sk_prd_123"),
				mupag.WithPrdEnvironment(),
				mupag.WithBaseURL("https://api.sandbox.mupag.com.br"),
			},
		},
		{
			name: "key with control character",
			options: []mupag.Option{
				mupag.WithAPIKey("sk_test_line\nbreak"),
				mupag.WithTestEnvironment(),
			},
		},
		{
			name: "key with non ascii character",
			options: []mupag.Option{
				mupag.WithAPIKey("sk_test_não-ascii"),
				mupag.WithTestEnvironment(),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := append(test.options, mupag.WithHTTPClient(&http.Client{Transport: transport}))
			client := mupag.NewClient(options...)
			if _, err := client.Charges.Create(context.Background(), validPixCharge()); err == nil {
				t.Fatal("unsafe client configuration was accepted")
			}
		})
	}
	if requests != 0 {
		t.Fatalf("network requests = %d, want 0", requests)
	}
}

func TestClientRejectsInvalidExplicitIdempotencyKeyBeforeNetwork(t *testing.T) {
	requests := 0
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return nil, errors.New("network must not be reached")
		})}),
	)

	for _, key := range []string{" ", strings.Repeat("a", 129), "line\nbreak", "non-ascii-ç"} {
		_, err := client.Charges.Create(
			context.Background(),
			mupag.ChargeCreateParams{},
			mupag.WithIdempotencyKey(key),
		)
		if err == nil {
			t.Fatalf("key %q was accepted", key)
		}
	}
	if requests != 0 {
		t.Fatalf("network requests = %d, want 0", requests)
	}
}

func TestSubscriptionCancelRejectsPathInjectionAndUnknownModeBeforeNetwork(t *testing.T) {
	requests := 0
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return nil, errors.New("network must not be reached")
		})}),
	)

	for _, params := range []struct {
		id   string
		mode string
	}{{"../charges", "immediate"}, {".", "immediate"}, {"..", "immediate"}, {"sub_123", "later"}} {
		_, err := client.Subscriptions.Cancel(
			context.Background(),
			params.id,
			mupag.CancelSubscriptionParams{Mode: params.mode},
		)
		if err == nil {
			t.Fatalf("cancel accepted id=%q mode=%q", params.id, params.mode)
		}
	}
	if requests != 0 {
		t.Fatalf("network requests = %d, want 0", requests)
	}
}

func TestRetryReusesGeneratedIdempotencyKeyAfterNetworkFailure(t *testing.T) {
	keys := []string{}
	attempts := 0
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithRetryPolicy(mupag.RetryPolicy{MaxRetries: 1}),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			keys = append(keys, request.Header.Get("Idempotency-Key"))
			attempts++
			if attempts == 1 {
				return nil, errors.New("connection reset")
			}
			return &http.Response{
				StatusCode: http.StatusCreated,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"charge_id":"ch_1","status":"pending","amount_cents":100}`)),
			}, nil
		})}),
	)

	if _, err := client.Charges.Create(context.Background(), validPixCharge()); err != nil {
		t.Fatalf("create charge: %v", err)
	}
	if len(keys) != 2 || keys[0] == "" || keys[0] != keys[1] {
		t.Fatalf("idempotency keys = %v", keys)
	}
}

func TestClientRejectsOversizedRequestAndResponse(t *testing.T) {
	requests := 0
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithMaxResponseBytes(64),
		mupag.WithRetryPolicy(mupag.RetryPolicy{MaxRetries: 0}),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Length": []string{"1000"}},
				Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", 1000))),
			}, nil
		})}),
	)

	oversized := validPixCharge()
	oversized.Metadata = map[string]any{"blob": strings.Repeat("x", 1024*1024+1)}
	_, err := client.Charges.Create(context.Background(), oversized)
	if err == nil || !strings.Contains(err.Error(), "request") {
		t.Fatalf("oversized request error = %v", err)
	}
	if requests != 0 {
		t.Fatalf("network requests after oversized request = %d", requests)
	}

	_, err = client.Subscriptions.Cancel(
		context.Background(),
		"sub_1",
		mupag.CancelSubscriptionParams{Mode: "immediate"},
	)
	if err == nil || !strings.Contains(err.Error(), "response") {
		t.Fatalf("oversized response error = %v", err)
	}
}

func TestChargeCreateRejectsUnsafeOrAmbiguousPayloadsBeforeNetwork(t *testing.T) {
	requests := 0
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return nil, errors.New("network must not be reached")
		})}),
	)

	invalid := []mupag.ChargeCreateParams{
		{AmountCents: 99, PaymentMethod: "pix", Customer: validCustomer()},
		{AmountCents: 100, PaymentMethod: "cash", Customer: validCustomer()},
		{AmountCents: 100, PaymentMethod: "pix", Customer: mupag.CustomerParams{}},
		{AmountCents: 100, PaymentMethod: "pix", Customer: validCustomer(), Metadata: map[string]any{"cvv": "123"}},
		{AmountCents: 100, PaymentMethod: "pix", Customer: validCustomer(), Metadata: map[string]any{"cardNumber": "4111111111111111"}},
		{AmountCents: 100, PaymentMethod: "pix", Customer: validCustomer(), Metadata: map[string]any{"nested": map[string]any{"security.code": "123"}}},
		{AmountCents: 100, PaymentMethod: "credit_card", Customer: validCustomer(), PayerIP: "203.0.113.10"},
		{AmountCents: 100, PaymentMethod: "pix", Customer: validCustomer(), CardTokenID: "tok_123"},
		{AmountCents: 100, PaymentMethod: "pix", Customer: validCustomer(), Installments: 1},
		{AmountCents: 100, PaymentMethod: "pix", Customer: validCustomer(), ProductMaxInstallments: 1},
		{AmountCents: 100, PaymentMethod: "pix", Customer: validCustomer(), SplitRules: []mupag.SplitRuleParams{{RecipientID: "recipient_1", ValueType: "fixed_amount"}}},
	}
	for index, params := range invalid {
		if _, err := client.Charges.Create(context.Background(), params); err == nil {
			t.Fatalf("invalid payload %d was accepted", index)
		}
	}
	if requests != 0 {
		t.Fatalf("network requests = %d, want 0", requests)
	}
}

func TestChargeCreateRejectsSensitiveMetadataKeyAliasesBeforeNetwork(t *testing.T) {
	requests := 0
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithRetryPolicy(mupag.RetryPolicy{MaxRetries: 0}),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return jsonHTTPResponse(http.StatusCreated, validChargeJSON()), nil
		})}),
	)

	for _, key := range []string{
		"cvv2", "cvc2", "cardCvv", "cardSecurityCode", "cardVerificationCode",
		"cvv_value", "cvcValue", "cardCvvCode", "cardCvcNumber",
		"cvv2_value", "cvc2Code", "cardCvv3Number",
		"cardVerificationNumber", "securityValue", "cardIdentificationNumber",
		"csc", "cid", "csc2", "cid3",
		"cardCsc", "cardCid", "csc_value", "cidCode", "cardCsc2Number", "cardCid3Value", "cardSecurityNumber",
	} {
		t.Run(key, func(t *testing.T) {
			params := validPixCharge()
			params.Metadata = map[string]any{key: "123"}
			if _, err := client.Charges.Create(context.Background(), params); err == nil {
				t.Fatalf("sensitive metadata key %q was accepted", key)
			}
		})
	}
	if requests != 0 {
		t.Fatalf("network requests = %d, want 0", requests)
	}
}

func TestChargeCreateAllowsNonCardSensitiveSuffixMetadataKeys(t *testing.T) {
	requests := 0
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithRetryPolicy(mupag.RetryPolicy{MaxRetries: 0}),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return jsonHTTPResponse(http.StatusCreated, validChargeJSON()), nil
		})}),
	)

	for _, key := range []string{
		"taxIdentificationNumber", "orderIdentificationNumber",
		"socialSecurityNumber", "accountSecurityNumber",
		"acid", "placid", "orderCidSuffix",
	} {
		t.Run(key, func(t *testing.T) {
			params := validPixCharge()
			params.Metadata = map[string]any{key: "identifier_123"}
			if _, err := client.Charges.Create(context.Background(), params); err != nil {
				t.Fatalf("non-card metadata key %q was rejected: %v", key, err)
			}
		})
	}
	if requests != 7 {
		t.Fatalf("network requests = %d, want 7", requests)
	}
}

func TestChargeCreateSendsTheValidatedMetadataSnapshot(t *testing.T) {
	var requestBody string
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithRetryPolicy(mupag.RetryPolicy{MaxRetries: 0}),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				return nil, err
			}
			requestBody = string(body)
			return jsonHTTPResponse(http.StatusCreated, validChargeJSON()), nil
		})}),
	)
	changingValue := &changingMetadataValue{}
	params := validPixCharge()
	params.Metadata = map[string]any{"note": changingValue}

	if _, err := client.Charges.Create(context.Background(), params); err != nil {
		t.Fatalf("create charge: %v", err)
	}
	if changingValue.calls != 1 {
		t.Fatalf("metadata marshaler calls = %d, want 1", changingValue.calls)
	}
	if !strings.Contains(requestBody, `"metadata":{"note":"validated"}`) || strings.Contains(requestBody, "4111111111111111") {
		t.Fatalf("request did not reuse validated metadata snapshot: %s", requestBody)
	}
}

func TestChargeCreateCanonicalizesTheValidatedMetadataSnapshot(t *testing.T) {
	var requestBody string
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithRetryPolicy(mupag.RetryPolicy{MaxRetries: 0}),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				return nil, err
			}
			requestBody = string(body)
			return jsonHTTPResponse(http.StatusCreated, validChargeJSON()), nil
		})}),
	)
	params := validPixCharge()
	params.Metadata = map[string]any{
		"nested": json.RawMessage(`{"note":"4111111111111111","note":"safe"}`),
	}

	if _, err := client.Charges.Create(context.Background(), params); err != nil {
		t.Fatalf("create charge: %v", err)
	}
	if strings.Contains(requestBody, "4111111111111111") || strings.Count(requestBody, `"note"`) != 1 {
		t.Fatalf("request did not canonicalize validated metadata snapshot: %s", requestBody)
	}
}

func TestChargeCreateRejectsPANInCardTokenFieldsBeforeNetwork(t *testing.T) {
	requests := 0
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithRetryPolicy(mupag.RetryPolicy{MaxRetries: 0}),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return jsonHTTPResponse(http.StatusCreated, validChargeJSON()), nil
		})}),
	)

	for _, test := range []struct {
		name        string
		cardToken   string
		cardTokenID string
	}{
		{name: "raw card token", cardToken: "4111111111111111"},
		{name: "stored card token id", cardTokenID: "4111111111111111"},
	} {
		t.Run(test.name, func(t *testing.T) {
			params := validPixCharge()
			params.PaymentMethod = "credit_card"
			params.PayerIP = "203.0.113.10"
			params.CardToken = test.cardToken
			params.CardTokenID = test.cardTokenID
			if _, err := client.Charges.Create(context.Background(), params); err == nil {
				t.Fatal("PAN-like card token was accepted")
			}
		})
	}
	if requests != 0 {
		t.Fatalf("network requests = %d, want 0", requests)
	}
}

func TestChargeCreateRejectsPANInFreeTextFieldsBeforeNetwork(t *testing.T) {
	requests := 0
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithRetryPolicy(mupag.RetryPolicy{MaxRetries: 0}),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return jsonHTTPResponse(http.StatusCreated, validChargeJSON()), nil
		})}),
	)
	tests := []struct {
		name  string
		apply func(*mupag.ChargeCreateParams)
	}{
		{name: "description", apply: func(params *mupag.ChargeCreateParams) { params.Description = "invoice 4111 1111 1111 1111" }},
		{name: "external reference", apply: func(params *mupag.ChargeCreateParams) { params.ExternalReference = "order_4111111111111111" }},
		{name: "affiliate code", apply: func(params *mupag.ChargeCreateParams) { params.AffiliateCode = "affiliate-4111111111111111" }},
		{name: "coupon code", apply: func(params *mupag.ChargeCreateParams) { params.CouponCode = "SAVE-4111111111111111" }},
		{name: "customer name", apply: func(params *mupag.ChargeCreateParams) { params.Customer.Name = "Ana 4111111111111111" }},
		{name: "customer email", apply: func(params *mupag.ChargeCreateParams) { params.Customer.Email = "4111111111111111@example.com" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params := validPixCharge()
			test.apply(&params)
			if _, err := client.Charges.Create(context.Background(), params); err == nil {
				t.Fatal("PAN-like free text was accepted")
			}
		})
	}
	if requests != 0 {
		t.Fatalf("network requests = %d, want 0", requests)
	}
}

func TestChargeCreateAllowsPANLikeDigitsInStructuredIdentityFields(t *testing.T) {
	requests := 0
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithRetryPolicy(mupag.RetryPolicy{MaxRetries: 0}),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return jsonHTTPResponse(http.StatusCreated, validChargeJSON()), nil
		})}),
	)
	params := validPixCharge()
	params.Customer.ID = "customer_4111111111111111"
	params.Customer.TaxID = "41234567890120"

	if _, err := client.Charges.Create(context.Background(), params); err != nil {
		t.Fatalf("structured identity fields were rejected: %v", err)
	}
	if requests != 1 {
		t.Fatalf("network requests = %d, want 1", requests)
	}
}

func TestChargeCreateRejectsPANLikeMetadataValuesRecursivelyBeforeNetwork(t *testing.T) {
	requests := 0
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithRetryPolicy(mupag.RetryPolicy{MaxRetries: 0}),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return jsonHTTPResponse(http.StatusCreated, validChargeJSON()), nil
		})}),
	)
	tests := []struct {
		name     string
		metadata map[string]any
	}{
		{
			name:     "JSON string with separators",
			metadata: map[string]any{"note": "4111 1111-1111 1111"},
		},
		{
			name:     "JSON string with punctuation separators",
			metadata: map[string]any{"note": "4111.1111/1111_1111"},
		},
		{
			name:     "JSON string with plus separators",
			metadata: map[string]any{"note": "4111+1111+1111+1111"},
		},
		{
			name:     "JSON string with equals separators",
			metadata: map[string]any{"note": "4111=1111=1111=1111"},
		},
		{
			name:     "JSON string with pipe separators",
			metadata: map[string]any{"note": "4111|1111|1111|1111"},
		},
		{
			name:     "formatted PAN after unrelated numeric metadata",
			metadata: map[string]any{"note": "order 9 / 4111 1111 1111 1111"},
		},
		{
			name:     "PAN inside a larger uninterrupted numeric group",
			metadata: map[string]any{"note": "94111111111111111"},
		},
		{
			name: "nested value",
			metadata: map[string]any{
				"order": []any{map[string]any{"note": "card 4111-1111-1111-1111"}},
			},
		},
		{
			name:     "exact JSON number",
			metadata: map[string]any{"note": int64(4111111111111111)},
		},
	}
	for _, pan := range []string{
		"412345678905",
		"4123456789011",
		"41234567890120",
		"412345678901233",
		"4123456789012349",
		"41234567890123458",
		"412345678901234561",
		"4123456789012345677",
	} {
		tests = append(tests,
			struct {
				name     string
				metadata map[string]any
			}{name: fmt.Sprintf("%d-digit PAN with continuous prefix", len(pan)), metadata: map[string]any{"note": "9" + pan}},
			struct {
				name     string
				metadata map[string]any
			}{name: fmt.Sprintf("%d-digit PAN with continuous suffix", len(pan)), metadata: map[string]any{"note": pan + "9"}},
		)
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params := validPixCharge()
			params.Metadata = test.metadata
			if _, err := client.Charges.Create(context.Background(), params); err == nil {
				t.Fatal("PAN-like metadata value was accepted")
			}
		})
	}
	if requests != 0 {
		t.Fatalf("network requests = %d, want 0", requests)
	}

	params := validPixCharge()
	params.Metadata = map[string]any{"note": "1000 0000 0000 1000"}
	if _, err := client.Charges.Create(context.Background(), params); err != nil {
		t.Fatalf("non-Luhn metadata was rejected: %v", err)
	}
	if requests != 1 {
		t.Fatalf("network requests = %d, want 1 after safe metadata", requests)
	}
}

func TestChargeCreateRequiresInlineIdentityAndLiteralPayerIPBeforeNetwork(t *testing.T) {
	requests := 0
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return nil, errors.New("network must not be reached")
		})}),
	)
	identity := mupag.CustomerParams{
		ID: "cus_123", Name: "Ana Silva", Email: "ana@example.com", TaxID: "12345678901",
	}
	card := mupag.ChargeCreateParams{
		AmountCents: 100, PaymentMethod: "credit_card", Customer: identity, CardTokenID: "tok_123",
	}
	invalid := []mupag.ChargeCreateParams{
		{AmountCents: 100, PaymentMethod: "pix", Customer: mupag.CustomerParams{ID: "cus_123"}},
		{AmountCents: 100, PaymentMethod: "pix", Customer: mupag.CustomerParams{ID: "cus_123", Name: "Ana", Email: "ana@example.com", TaxID: "123"}},
		card,
		func() mupag.ChargeCreateParams { value := card; value.PayerIP = "payer.example.com"; return value }(),
		func() mupag.ChargeCreateParams {
			value := card
			value.PayerIP = "203.0.113.10"
			value.Installments = 2
			return value
		}(),
		func() mupag.ChargeCreateParams {
			value := card
			value.PayerIP = "203.0.113.10"
			value.ProductMaxInstallments = 2
			return value
		}(),
		{AmountCents: 100, PaymentMethod: "pix", Customer: identity, SoftDescriptor: "MUPAG"},
		{AmountCents: 100, PaymentMethod: "pix", Customer: identity, SoftDescriptor: " "},
	}

	for index, params := range invalid {
		if _, err := client.Charges.Create(context.Background(), params); err == nil {
			t.Fatalf("invalid contract payload %d was accepted", index)
		}
	}
	if requests != 0 {
		t.Fatalf("network requests = %d, want 0", requests)
	}
}

func validPixCharge() mupag.ChargeCreateParams {
	return mupag.ChargeCreateParams{
		AmountCents:   100,
		PaymentMethod: "pix",
		Customer:      validCustomer(),
	}
}

func validCustomer() mupag.CustomerParams {
	return mupag.CustomerParams{
		ID: "cus_123", Name: "Ana Silva", Email: "ana@example.com", TaxID: "12345678901",
	}
}

func TestClientBoundsAndCancellationAreEnforced(t *testing.T) {
	client := mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithRetryPolicy(mupag.RetryPolicy{MaxRetries: 6, BaseDelay: time.Hour}),
	)
	_, err := client.Charges.Create(context.Background(), mupag.ChargeCreateParams{})
	if err == nil || !strings.Contains(err.Error(), "retry") {
		t.Fatalf("retry configuration error = %v", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	client = mupag.NewClient(
		mupag.WithAPIKey("sk_test_123"),
		mupag.WithTestEnvironment(),
		mupag.WithRetryPolicy(mupag.RetryPolicy{MaxRetries: 1, BaseDelay: time.Second}),
		mupag.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("temporary")
		})}),
	)
	started := time.Now()
	_, err = client.Charges.Create(canceled, mupag.ChargeCreateParams{})
	if err == nil || time.Since(started) > 100*time.Millisecond {
		t.Fatalf("canceled request err=%v duration=%s", err, time.Since(started))
	}
}

func TestWebhookRejectsOversizedOrStructurallyInvalidPayload(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	oversized := []byte(strings.Repeat("x", 1024*1024+1))
	if _, err := mupag.Webhooks.ConstructEvent(oversized, "t=1700000000,v1="+strings.Repeat("0", 64), "whsec_123", mupag.WithWebhookNow(now)); err == nil {
		t.Fatal("expected oversized payload error")
	}

	payload := []byte(`{"data":{}}`)
	header := signatureHeader(now, payload, "whsec_123")
	if _, err := mupag.Webhooks.ConstructEvent(payload, header, "whsec_123", mupag.WithWebhookNow(now)); err == nil {
		t.Fatal("expected missing id/type error")
	}
}
