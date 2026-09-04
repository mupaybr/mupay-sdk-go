package mupag

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

const maxWebhookBytes = 1024 * 1024

// Webhooks expoe helpers stateless para validar webhooks recebidos.
var Webhooks webhookVerifier

type webhookVerifier struct{}

// WebhookEvent e o envelope validado de um webhook publico.
type WebhookEvent struct {
	ID   string          `json:"id"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type webhookOptions struct {
	now       time.Time
	tolerance time.Duration
}

// WebhookOption ajusta validacao sem desativar HMAC ou tolerancia.
type WebhookOption func(*webhookOptions)

// WithWebhookNow injeta relogio em testes e validacoes deterministicas.
func WithWebhookNow(now time.Time) WebhookOption {
	return func(options *webhookOptions) {
		options.now = now
	}
}

// WithWebhookTolerance define a janela maxima contra replay.
func WithWebhookTolerance(tolerance time.Duration) WebhookOption {
	return func(options *webhookOptions) {
		options.tolerance = tolerance
	}
}

// ConstructEvent valida timestamp + HMAC antes de expor dados do payload.
func (webhookVerifier) ConstructEvent(payload []byte, signatureHeader string, secret string, opts ...WebhookOption) (*WebhookEvent, error) {
	if len(payload) > maxWebhookBytes {
		return nil, errors.New("webhook payload exceeds 1 MiB")
	}
	if len(secret) < 1 || len(secret) > 512 || strings.TrimSpace(secret) != secret {
		return nil, errors.New("invalid webhook secret")
	}
	options := webhookOptions{
		now:       time.Now().UTC(),
		tolerance: 5 * time.Minute,
	}
	for _, opt := range opts {
		opt(&options)
	}
	if options.tolerance <= 0 || options.tolerance > 24*time.Hour {
		return nil, errors.New("invalid webhook tolerance")
	}
	parts, validHeader := parseSignatureHeader(signatureHeader)
	if !validHeader {
		return nil, errors.New("invalid webhook signature header")
	}
	timestampText := parts["t"]
	signature := parts["v1"]
	if timestampText == "" || signature == "" {
		return nil, errors.New("invalid webhook signature header")
	}
	if len(signature) != sha256.Size*2 {
		return nil, errors.New("invalid webhook signature")
	}
	if _, err := hex.DecodeString(signature); err != nil {
		return nil, errors.New("invalid webhook signature")
	}
	timestamp, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil || timestamp <= 0 || strconv.FormatInt(timestamp, 10) != timestampText {
		return nil, errors.New("invalid webhook timestamp")
	}
	signedAt := time.Unix(timestamp, 0)
	if signedAt.Before(options.now.Add(-options.tolerance)) || signedAt.After(options.now.Add(options.tolerance)) {
		return nil, errors.New("webhook timestamp outside tolerance")
	}
	expected := webhookSignature(timestampText, payload, secret)
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return nil, errors.New("invalid webhook signature")
	}
	var event WebhookEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, errors.New("invalid webhook payload")
	}
	if len(event.ID) < 1 || len(event.ID) > 256 || len(event.Type) < 1 || len(event.Type) > 128 {
		return nil, errors.New("invalid webhook payload")
	}
	var data map[string]json.RawMessage
	if len(event.Data) == 0 || json.Unmarshal(event.Data, &data) != nil || data == nil {
		return nil, errors.New("invalid webhook payload")
	}
	return &event, nil
}

func parseSignatureHeader(header string) (map[string]string, bool) {
	parts := map[string]string{}
	if len(header) > 4096 {
		return parts, false
	}
	pieces := strings.Split(header, ",")
	if len(pieces) > 16 {
		return parts, false
	}
	for _, piece := range pieces {
		key, value, ok := strings.Cut(piece, "=")
		if !ok {
			return parts, false
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			return parts, false
		}
		if key == "t" || key == "v1" {
			if _, duplicate := parts[key]; duplicate {
				return parts, false
			}
			parts[key] = value
		}
	}
	return parts, true
}

func webhookSignature(timestamp string, payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}
