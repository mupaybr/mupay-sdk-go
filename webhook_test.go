package mupaysdk_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"
	"time"

	mupaysdk "github.com/marcosvbarra/mupay/sdks/go"
)

func TestWebhookConstructEventAcceptsFreshValidSignature(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	payload := []byte(`{"id":"evt_123","type":"charge.paid","data":{"charge_id":"ch_123"}}`)
	header := signatureHeader(now, payload, "whsec_123")

	event, err := mupaysdk.Webhooks.ConstructEvent(payload, header, "whsec_123", mupaysdk.WithWebhookNow(now))
	if err != nil {
		t.Fatalf("construct event: %v", err)
	}
	if event.ID != "evt_123" || event.Type != "charge.paid" {
		t.Fatalf("event = %+v", event)
	}
	if string(event.Data) != `{"charge_id":"ch_123"}` {
		t.Fatalf("data = %s", event.Data)
	}
}

func TestWebhookConstructEventRejectsInvalidSignature(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	payload := []byte(`{"id":"evt_123","type":"charge.paid"}`)
	header := signatureHeader(now, payload, "whsec_123")

	_, err := mupaysdk.Webhooks.ConstructEvent(payload, header, "wrong_secret", mupaysdk.WithWebhookNow(now))
	if err == nil {
		t.Fatal("expected invalid signature error")
	}
}

func TestWebhookConstructEventRejectsStaleTimestamp(t *testing.T) {
	signedAt := time.Unix(1_700_000_000, 0)
	now := signedAt.Add(6 * time.Minute)
	payload := []byte(`{"id":"evt_123","type":"charge.paid"}`)
	header := signatureHeader(signedAt, payload, "whsec_123")

	_, err := mupaysdk.Webhooks.ConstructEvent(payload, header, "whsec_123", mupaysdk.WithWebhookNow(now))
	if err == nil {
		t.Fatal("expected stale timestamp error")
	}
}

func TestWebhookConstructEventAcceptsCustomTolerance(t *testing.T) {
	signedAt := time.Unix(1_700_000_000, 0)
	now := signedAt.Add(6 * time.Minute)
	payload := []byte(`{"id":"evt_123","type":"charge.paid"}`)
	header := signatureHeader(signedAt, payload, "whsec_123")

	event, err := mupaysdk.Webhooks.ConstructEvent(
		payload,
		header,
		"whsec_123",
		mupaysdk.WithWebhookNow(now),
		mupaysdk.WithWebhookTolerance(10*time.Minute),
	)
	if err != nil {
		t.Fatalf("construct event: %v", err)
	}
	if event.ID != "evt_123" {
		t.Fatalf("event = %+v", event)
	}
}

func TestWebhookConstructEventRejectsMalformedInputs(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	payload := []byte(`{"id":"evt_123","type":"charge.paid"}`)

	if _, err := mupaysdk.Webhooks.ConstructEvent(payload, "v1=abc", "whsec_123", mupaysdk.WithWebhookNow(now)); err == nil {
		t.Fatal("expected missing timestamp error")
	}
	if _, err := mupaysdk.Webhooks.ConstructEvent(payload, "t=not-a-number,v1=abc", "whsec_123", mupaysdk.WithWebhookNow(now)); err == nil {
		t.Fatal("expected invalid timestamp error")
	}

	header := signatureHeader(now, []byte(`not-json`), "whsec_123")
	if _, err := mupaysdk.Webhooks.ConstructEvent([]byte(`not-json`), header, "whsec_123", mupaysdk.WithWebhookNow(now)); err == nil {
		t.Fatal("expected invalid payload error")
	}
}

func signatureHeader(ts time.Time, payload []byte, secret string) string {
	timestamp := strconv.FormatInt(ts.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(payload)
	return "t=" + timestamp + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}
