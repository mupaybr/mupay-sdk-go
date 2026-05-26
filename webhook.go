package mupaysdk

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
	options := webhookOptions{
		now:       time.Now().UTC(),
		tolerance: 5 * time.Minute,
	}
	for _, opt := range opts {
		opt(&options)
	}
	parts := parseSignatureHeader(signatureHeader)
	timestampText := parts["t"]
	signature := parts["v1"]
	if timestampText == "" || signature == "" {
		return nil, errors.New("invalid webhook signature header")
	}
	timestamp, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil {
		return nil, errors.New("invalid webhook timestamp")
	}
	signedAt := time.Unix(timestamp, 0)
	age := options.now.Sub(signedAt)
	if age < 0 {
		age = -age
	}
	if age > options.tolerance {
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
	return &event, nil
}

func parseSignatureHeader(header string) map[string]string {
	parts := map[string]string{}
	for _, piece := range strings.Split(header, ",") {
		key, value, ok := strings.Cut(piece, "=")
		if ok {
			parts[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return parts
}

func webhookSignature(timestamp string, payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}
