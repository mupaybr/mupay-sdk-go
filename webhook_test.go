package mupag_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"

	mupag "github.com/mupaybr/mupag-sdk-go"
)

func TestWebhookConstructEventAcceptsFreshValidSignature(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	payload := []byte(`{"id":"evt_123","type":"charge.paid","data":{"charge_id":"ch_123"}}`)
	header := signatureHeader(now, payload, "whsec_123")

	event, err := mupag.Webhooks.ConstructEvent(payload, header, "whsec_123", mupag.WithWebhookNow(now))
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
	payload := []byte(`{"id":"evt_123","type":"charge.paid","data":{}}`)
	header := signatureHeader(now, payload, "whsec_123")

	_, err := mupag.Webhooks.ConstructEvent(payload, header, "wrong_secret", mupag.WithWebhookNow(now))
	if err == nil {
		t.Fatal("expected invalid signature error")
	}
}

func TestWebhookConstructEventRejectsNonObjectData(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	for _, payload := range [][]byte{
		[]byte(`{"id":"evt_123","type":"charge.paid"}`),
		[]byte(`{"id":"evt_123","type":"charge.paid","data":null}`),
		[]byte(`{"id":"evt_123","type":"charge.paid","data":[]}`),
	} {
		header := signatureHeader(now, payload, "whsec_123")
		if _, err := mupag.Webhooks.ConstructEvent(payload, header, "whsec_123", mupag.WithWebhookNow(now)); err == nil {
			t.Fatalf("accepted non-object data: %s", payload)
		}
	}
}

func TestWebhookConstructEventRejectsStaleTimestamp(t *testing.T) {
	signedAt := time.Unix(1_700_000_000, 0)
	now := signedAt.Add(6 * time.Minute)
	payload := []byte(`{"id":"evt_123","type":"charge.paid"}`)
	header := signatureHeader(signedAt, payload, "whsec_123")

	_, err := mupag.Webhooks.ConstructEvent(payload, header, "whsec_123", mupag.WithWebhookNow(now))
	if err == nil {
		t.Fatal("expected stale timestamp error")
	}
}

func TestWebhookConstructEventRejectsFarFutureTimestampWithoutDurationOverflow(t *testing.T) {
	now := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	signedAt := time.Date(2340, time.September, 1, 0, 0, 0, 0, time.UTC)
	payload := []byte(`{"id":"evt_123","type":"charge.paid","data":{"charge_id":"charge_1"}}`)
	header := signatureHeader(signedAt, payload, "whsec_123")

	if _, err := mupag.Webhooks.ConstructEvent(payload, header, "whsec_123", mupag.WithWebhookNow(now)); err == nil {
		t.Fatal("accepted far-future webhook timestamp")
	}
}

func TestWebhookConstructEventAcceptsCustomTolerance(t *testing.T) {
	signedAt := time.Unix(1_700_000_000, 0)
	now := signedAt.Add(6 * time.Minute)
	payload := []byte(`{"id":"evt_123","type":"charge.paid","data":{}}`)
	header := signatureHeader(signedAt, payload, "whsec_123")

	event, err := mupag.Webhooks.ConstructEvent(
		payload,
		header,
		"whsec_123",
		mupag.WithWebhookNow(now),
		mupag.WithWebhookTolerance(10*time.Minute),
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

	if _, err := mupag.Webhooks.ConstructEvent(payload, "v1=abc", "whsec_123", mupag.WithWebhookNow(now)); err == nil {
		t.Fatal("expected missing timestamp error")
	}
	if _, err := mupag.Webhooks.ConstructEvent(payload, "t=not-a-number,v1=abc", "whsec_123", mupag.WithWebhookNow(now)); err == nil {
		t.Fatal("expected invalid timestamp error")
	}
	valid := signatureHeader(now, payload, "whsec_123")
	for _, header := range []string{
		"t=1e3,v1=" + strings.Repeat("a", 64),
		valid + ",t=" + strconv.FormatInt(now.Unix(), 10),
		valid + ",v1=" + strings.Repeat("a", 64),
	} {
		if _, err := mupag.Webhooks.ConstructEvent(payload, header, "whsec_123", mupag.WithWebhookNow(now)); err == nil {
			t.Fatalf("accepted ambiguous signature header: %s", header)
		}
	}

	header := signatureHeader(now, []byte(`not-json`), "whsec_123")
	if _, err := mupag.Webhooks.ConstructEvent([]byte(`not-json`), header, "whsec_123", mupag.WithWebhookNow(now)); err == nil {
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
