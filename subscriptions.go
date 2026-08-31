package mupag

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// SubscriptionsService agrupa operacoes de assinaturas da API publica.
type SubscriptionsService struct {
	client *Client
}

// Subscription representa o estado resumido de uma assinatura.
type Subscription struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func (subscription *Subscription) validateResponse() error {
	if subscription == nil || !validResourceID(subscription.ID) {
		return errors.New("mupag: API returned an invalid subscription id")
	}
	if subscription.Status == "" || len(subscription.Status) > 64 {
		return errors.New("mupag: API returned an invalid subscription status")
	}
	return nil
}

// CancelSubscriptionParams reflete o corpo exigido pelo endpoint real de cancelamento.
type CancelSubscriptionParams struct {
	Mode   string `json:"mode"`
	Reason string `json:"reason,omitempty"`
}

// Cancel encerra uma assinatura usando POST idempotente.
func (service *SubscriptionsService) Cancel(ctx context.Context, id string, params CancelSubscriptionParams, opts ...RequestOption) (*Subscription, error) {
	if !validResourceID(id) {
		return nil, errors.New("mupag: invalid subscription id")
	}
	if params.Mode != "immediate" && params.Mode != "end_of_period" {
		return nil, errors.New("mupag: subscription cancel mode must be immediate or end_of_period")
	}
	if len(params.Reason) > 500 {
		return nil, errors.New("mupag: subscription cancel reason exceeds 500 bytes")
	}
	var subscription Subscription
	path := "/v1/subscriptions/" + url.PathEscape(id) + "/cancel"
	err := service.client.do(ctx, http.MethodPost, path, nil, params, &subscription, opts...)
	if err != nil {
		return nil, err
	}
	return &subscription, nil
}

func validResourceID(value string) bool {
	if len(value) < 1 || len(value) > 256 {
		return false
	}
	for _, character := range []byte(value) {
		if !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' || character == '.' || character == '~') {
			return false
		}
	}
	return true
}
