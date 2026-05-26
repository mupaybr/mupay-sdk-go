package mupaysdk

import (
	"context"
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

// CancelSubscriptionParams reflete o corpo exigido pelo endpoint real de cancelamento.
type CancelSubscriptionParams struct {
	Mode   string `json:"mode"`
	Reason string `json:"reason,omitempty"`
}

// Cancel encerra uma assinatura usando POST idempotente.
func (service *SubscriptionsService) Cancel(ctx context.Context, id string, params CancelSubscriptionParams, opts ...RequestOption) (*Subscription, error) {
	var subscription Subscription
	path := "/v1/subscriptions/" + url.PathEscape(id) + "/cancel"
	err := service.client.do(ctx, http.MethodPost, path, nil, params, &subscription, opts...)
	if err != nil {
		return nil, err
	}
	return &subscription, nil
}
