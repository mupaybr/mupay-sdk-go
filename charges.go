package mupaysdk

import (
	"context"
	"net/http"
	"time"
)

// ChargesService agrupa operacoes de cobranca da API publica.
type ChargesService struct {
	client *Client
}

// CustomerParams identifica o comprador sem aceitar campos internos.
type CustomerParams struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	TaxID string `json:"tax_id,omitempty"`
}

// ChargeCreateParams representa o payload publico para criar cobranca.
type ChargeCreateParams struct {
	AmountCents            int64             `json:"amount_cents"`
	PaymentMethod          string            `json:"payment_method"`
	Installments           int               `json:"installments,omitempty"`
	CardToken              string            `json:"card_token,omitempty"`
	CardTokenID            string            `json:"card_token_id,omitempty"`
	SaveCard               bool              `json:"save_card,omitempty"`
	Customer               CustomerParams    `json:"customer"`
	Description            string            `json:"description,omitempty"`
	SoftDescriptor         string            `json:"soft_descriptor,omitempty"`
	AuthOnly               bool              `json:"auth_only,omitempty"`
	ProductMaxInstallments int               `json:"product_max_installments,omitempty"`
	ExternalReference      string            `json:"external_reference,omitempty"`
	ExpiresInSeconds       int               `json:"expires_in_seconds,omitempty"`
	Metadata               map[string]any    `json:"metadata,omitempty"`
	AffiliateCode          string            `json:"affiliate_code,omitempty"`
	CouponCode             string            `json:"coupon_code,omitempty"`
	SplitRules             []SplitRuleParams `json:"split_rules,omitempty"`
	IsMIT                  bool              `json:"is_mit,omitempty"`
	InitialMITReferenceID  string            `json:"initial_mit_reference_id,omitempty"`
}

// SplitRuleParams descreve uma regra publica de split enviada ao BaaS.
type SplitRuleParams struct {
	RecipientID string `json:"recipient_id,omitempty"`
	ValueType   string `json:"value_type,omitempty"`
	ValueBPS    int    `json:"value_bps,omitempty"`
	ValueCents  int64  `json:"value_cents,omitempty"`
}

// Charge representa uma cobranca retornada pela API publica.
type Charge struct {
	ChargeID              string     `json:"charge_id"`
	Status                string     `json:"status"`
	AmountCents           int64      `json:"amount_cents"`
	PSPChargeID           string     `json:"psp_charge_id"`
	CardTokenID           string     `json:"card_token_id,omitempty"`
	CardBrand             string     `json:"card_brand,omitempty"`
	CardLast4             string     `json:"card_last4,omitempty"`
	ThreeDSACSURL         string     `json:"three_ds_acs_url,omitempty"`
	FailureClassification string     `json:"failure_classification,omitempty"`
	PixQRCodeBase64       string     `json:"pix_qr_code_base64,omitempty"`
	PixEMVCode            string     `json:"pix_emv_code,omitempty"`
	ExpiresAt             *time.Time `json:"expires_at,omitempty"`
}

// Create envia uma cobranca e gera Idempotency-Key quando o caller omite.
func (service *ChargesService) Create(ctx context.Context, params ChargeCreateParams, opts ...RequestOption) (*Charge, error) {
	var charge Charge
	err := service.client.do(ctx, http.MethodPost, "/v1/charges", nil, params, &charge, opts...)
	if err != nil {
		return nil, err
	}
	return &charge, nil
}
