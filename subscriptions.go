package mupag

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// SubscriptionsService agrupa operacoes de assinaturas da API publica.
type SubscriptionsService struct {
	client *Client
}

// Subscription representa o estado resumido de uma assinatura.
type Subscription struct {
	ID                string `json:"id"`
	Status            string `json:"status"`
	CancelAtPeriodEnd bool   `json:"cancel_at_period_end"`
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

type subscriptionCancelResponse struct {
	Subscription
	expectedID               string
	expectedMode             string
	expectedReason           string
	cancelAtPeriodEndPresent bool
	cancellationReason       json.RawMessage
}

func (response *subscriptionCancelResponse) UnmarshalJSON(payload []byte) error {
	var decoded struct {
		ID                 string          `json:"id"`
		Status             string          `json:"status"`
		CancelAtPeriodEnd  *bool           `json:"cancel_at_period_end"`
		CancellationReason json.RawMessage `json:"cancellation_reason"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return err
	}

	response.Subscription = Subscription{ID: decoded.ID, Status: decoded.Status}
	response.cancelAtPeriodEndPresent = decoded.CancelAtPeriodEnd != nil
	response.cancellationReason = decoded.CancellationReason
	if decoded.CancelAtPeriodEnd != nil {
		response.CancelAtPeriodEnd = *decoded.CancelAtPeriodEnd
	}
	return nil
}

func (response *subscriptionCancelResponse) validateResponse() error {
	if err := response.Subscription.validateResponse(); err != nil {
		return err
	}
	if response.ID != response.expectedID {
		return errors.New("mupag: API returned a different subscription")
	}
	if !response.cancelAtPeriodEndPresent {
		return errors.New("mupag: API returned a subscription without cancel_at_period_end")
	}
	if !rawOptionalStringEchoMatches(response.cancellationReason, response.expectedReason) {
		return errors.New("mupag: API returned a subscription cancellation reason that does not match the request")
	}
	switch response.expectedMode {
	case "immediate":
		if response.Status != "canceled" || response.CancelAtPeriodEnd {
			return errors.New("mupag: API returned a subscription incompatible with immediate cancellation")
		}
	case "end_of_period":
		if !scheduledCancelStatus(response.Status) || !response.CancelAtPeriodEnd {
			return errors.New("mupag: API returned a subscription incompatible with end-of-period cancellation")
		}
	default:
		return errors.New("mupag: API returned a subscription for an unknown cancellation mode")
	}
	return nil
}

func scheduledCancelStatus(status string) bool {
	switch status {
	case "trialing", "active", "past_due", "unpaid", "paused", "incomplete":
		return true
	default:
		return false
	}
}

// CancelSubscriptionParams reflete o corpo exigido pelo endpoint real de cancelamento.
type CancelSubscriptionParams struct {
	Mode   string `json:"mode"`
	Reason string `json:"reason,omitempty"`
}

// Cancel encerra uma assinatura usando POST idempotente.
func (service *SubscriptionsService) Cancel(ctx context.Context, id string, params CancelSubscriptionParams, opts ...RequestOption) (*Subscription, error) {
	if !validResourceID(id) || containsPANLikeSequence(id) {
		return nil, errors.New("mupag: invalid subscription id")
	}
	params.Reason = strings.TrimFunc(params.Reason, func(character rune) bool {
		return character <= ' '
	})
	if params.Mode != "immediate" && params.Mode != "end_of_period" {
		return nil, errors.New("mupag: subscription cancel mode must be immediate or end_of_period")
	}
	if len(params.Reason) > 500 {
		return nil, errors.New("mupag: subscription cancel reason exceeds 500 bytes")
	}
	if containsPANLikeSequence(params.Reason) {
		return nil, errors.New("mupag: subscription cancel reason cannot contain a payment card number")
	}
	response := subscriptionCancelResponse{expectedID: id, expectedMode: params.Mode, expectedReason: params.Reason}
	path := "/v1/subscriptions/" + url.PathEscape(id) + "/cancel"
	err := service.client.do(ctx, http.MethodPost, path, nil, params, &response, opts...)
	if err != nil {
		return nil, err
	}
	return &response.Subscription, nil
}

func validResourceID(value string) bool {
	if len(value) < 1 || len(value) > 256 {
		return false
	}
	if value == "." || value == ".." {
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
