package mupag

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
	PayerIP                string            `json:"payer_ip,omitempty"`
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
	CreatedAt             *time.Time `json:"created_at,omitempty"`
	PixQRCode             string     `json:"pix_qr_code,omitempty"`
	PixCopyPaste          string     `json:"pix_copy_paste,omitempty"`
}

func (charge *Charge) validateResponse() error {
	if charge == nil || !validResourceID(charge.ChargeID) {
		return errors.New("mupag: API returned an invalid charge_id")
	}
	validStatuses := map[string]struct{}{
		"created": {}, "pending": {}, "authorized": {}, "under_review": {}, "paid": {},
		"partially_refunded": {}, "refunded": {}, "failed": {}, "expired": {},
		"cancelled": {}, "disputed": {}, "chargeback": {},
	}
	if _, ok := validStatuses[charge.Status]; !ok {
		return errors.New("mupag: API returned an invalid charge status")
	}
	if charge.AmountCents < 1 || charge.AmountCents > maxMoneyCents {
		return errors.New("mupag: API returned an invalid charge amount")
	}
	return nil
}

type chargeCreateResponse struct {
	Charge
	expectedAmountCents int64
	allowDiscount       bool
}

func (response *chargeCreateResponse) validateResponse() error {
	if err := response.Charge.validateResponse(); err != nil {
		return err
	}
	if (response.allowDiscount && response.AmountCents > response.expectedAmountCents) ||
		(!response.allowDiscount && response.AmountCents != response.expectedAmountCents) {
		return errors.New("mupag: API returned a charge amount that does not match the request")
	}
	return nil
}

// ChargeListParams representa filtros bounded do endpoint público de cobranças.
type ChargeListParams struct {
	Status        string
	CustomerID    string
	PaymentMethod string
	CreatedAtFrom *time.Time
	CreatedAtTo   *time.Time
	Limit         int
	Cursor        string
}

// ChargePage representa uma única página keyset de cobranças.
type ChargePage struct {
	Data       []Charge `json:"data"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

func (page *ChargePage) validateResponse() error {
	if page == nil {
		return errors.New("mupag: API returned an invalid charge page")
	}
	if len(page.Data) > 100 {
		return errors.New("mupag: API returned more than 100 charges in one page")
	}
	if page.NextCursor != "" {
		if err := validateOpaqueCursor(page.NextCursor); err != nil {
			return errors.New("mupag: API returned invalid next_cursor")
		}
	}
	for index := range page.Data {
		if err := page.Data[index].validateResponse(); err != nil {
			return err
		}
	}
	return nil
}

type chargeListResponse struct {
	Data                   *[]Charge `json:"data"`
	NextCursor             string    `json:"next_cursor,omitempty"`
	expectedStatus         string
	expectedCreatedAtFrom  time.Time
	expectedCreatedAtTo    time.Time
	expectedLimit          int
	correlateCreatedAtFrom bool
	correlateCreatedAtTo   bool
}

func (response *chargeListResponse) validateResponse() error {
	if response == nil || response.Data == nil {
		return errors.New("mupag: API returned an invalid charge page")
	}
	page := response.page()
	if err := page.validateResponse(); err != nil {
		return err
	}
	if len(page.Data) > response.expectedLimit {
		return errors.New("mupag: API returned more charges than the requested page limit")
	}
	for index := range page.Data {
		charge := &page.Data[index]
		if charge.CreatedAt == nil {
			return errors.New("mupag: API returned a charge without created_at")
		}
		if response.expectedStatus != "" && charge.Status != response.expectedStatus {
			return errors.New("mupag: API returned a charge outside the requested status")
		}
		if response.correlateCreatedAtFrom && charge.CreatedAt.Before(response.expectedCreatedAtFrom) {
			return errors.New("mupag: API returned a charge before created_at_from")
		}
		if response.correlateCreatedAtTo && !charge.CreatedAt.Before(response.expectedCreatedAtTo) {
			return errors.New("mupag: API returned a charge at or after created_at_to")
		}
	}
	return nil
}

func (response *chargeListResponse) page() *ChargePage {
	return &ChargePage{
		Data:       *response.Data,
		NextCursor: response.NextCursor,
	}
}

// Create envia uma cobranca e gera Idempotency-Key quando o caller omite.
func (service *ChargesService) Create(ctx context.Context, params ChargeCreateParams, opts ...RequestOption) (*Charge, error) {
	if service.client.configErr != nil {
		return nil, service.client.configErr
	}
	requestOptions := requestOptions{}
	for _, option := range opts {
		option(&requestOptions)
	}
	if requestOptions.idempotencyKeySet {
		if err := validateIdempotencyKey(requestOptions.idempotencyKey); err != nil {
			return nil, err
		}
	}
	if err := validateChargeCreateParams(params); err != nil {
		return nil, err
	}
	response := chargeCreateResponse{
		expectedAmountCents: params.AmountCents,
		allowDiscount:       strings.TrimSpace(params.CouponCode) != "",
	}
	err := service.client.do(ctx, http.MethodPost, "/v1/charges", nil, params, &response, opts...)
	if err != nil {
		return nil, err
	}
	return &response.Charge, nil
}

func validateChargeCreateParams(params ChargeCreateParams) error {
	if params.AmountCents < 100 || params.AmountCents > maxMoneyCents {
		return errors.New("mupag: amount_cents must be between 100 and 9000000000000000")
	}
	if params.PaymentMethod != "pix" && params.PaymentMethod != "credit_card" {
		return errors.New("mupag: payment_method must be pix or credit_card")
	}
	if params.Customer.ID != "" && !validResourceID(params.Customer.ID) {
		return errors.New("mupag: invalid customer id")
	}
	if invalidText(params.Customer.Name, 200) || invalidText(params.Customer.Email, 254) || !validEmail(params.Customer.Email) || !validTaxID(params.Customer.TaxID) {
		return errors.New("mupag: customer name, email and 11/14-digit tax_id are required")
	}
	if params.Installments < 0 || params.Installments > 1 {
		return errors.New("mupag: installments must be 1 when provided")
	}
	if params.ProductMaxInstallments < 0 || params.ProductMaxInstallments > 1 {
		return errors.New("mupag: product_max_installments must be 1 when provided")
	}
	if params.ExpiresInSeconds != 0 && params.ExpiresInSeconds < 60 {
		return errors.New("mupag: expires_in_seconds must be at least 60")
	}
	if params.Description != "" && invalidText(params.Description, 500) {
		return errors.New("mupag: invalid description")
	}
	if params.SoftDescriptor != "" {
		return errors.New("mupag: soft_descriptor is not supported by Asaas")
	}
	if params.PayerIP != "" && net.ParseIP(params.PayerIP) == nil {
		return errors.New("mupag: payer_ip must be an IPv4 or IPv6 literal")
	}
	if params.AuthOnly {
		return errors.New("mupag: auth_only is not supported")
	}
	if err := validateMetadata(params.Metadata); err != nil {
		return err
	}
	hasRawToken := params.CardToken != ""
	hasStoredToken := params.CardTokenID != ""
	if params.PaymentMethod == "credit_card" && params.PayerIP == "" {
		return errors.New("mupag: credit_card requires payer_ip")
	}
	if hasRawToken && (len(params.CardToken) > 4096 || strings.ContainsAny(params.CardToken, "\r\n\x00")) {
		return errors.New("mupag: invalid card_token")
	}
	if hasStoredToken && !validResourceID(params.CardTokenID) {
		return errors.New("mupag: invalid card_token_id")
	}
	if params.PaymentMethod == "credit_card" && hasRawToken == hasStoredToken {
		return errors.New("mupag: credit_card requires exactly card_token or card_token_id")
	}
	if params.PaymentMethod == "pix" && (hasRawToken || hasStoredToken || params.SaveCard) {
		return errors.New("mupag: card fields are not allowed for pix")
	}
	if params.IsMIT {
		if !hasStoredToken || hasRawToken || !validResourceID(params.InitialMITReferenceID) {
			return errors.New("mupag: MIT requires card_token_id and initial_mit_reference_id without raw card_token")
		}
	} else if params.InitialMITReferenceID != "" {
		return errors.New("mupag: initial_mit_reference_id requires is_mit")
	}
	return validateSplitRules(params.SplitRules, params.AmountCents)
}

func validateSplitRules(rules []SplitRuleParams, amountCents int64) error {
	if len(rules) > 50 {
		return errors.New("mupag: split_rules supports at most 50 entries")
	}
	allocatedCents := int64(0)
	totalBPS := 0
	for _, rule := range rules {
		if !validResourceID(rule.RecipientID) {
			return errors.New("mupag: invalid split recipient_id")
		}
		var ruleCents int64
		switch rule.ValueType {
		case "fixed_amount":
			if rule.ValueCents <= 0 || rule.ValueCents > amountCents || rule.ValueBPS != 0 {
				return errors.New("mupag: fixed_amount split requires only value_cents")
			}
			ruleCents = rule.ValueCents
		case "percentage_of_gross":
			if rule.ValueBPS <= 0 || rule.ValueBPS > 10_000 || rule.ValueCents != 0 {
				return errors.New("mupag: percentage split requires only value_bps between 1 and 10000")
			}
			if totalBPS > 10_000-rule.ValueBPS {
				return errors.New("mupag: aggregate split allocation exceeds charge amount or 100 percent")
			}
			totalBPS += rule.ValueBPS
			ruleCents = splitPercentageCents(amountCents, rule.ValueBPS)
		default:
			return errors.New("mupag: invalid split value_type")
		}
		if allocatedCents > amountCents-ruleCents {
			return errors.New("mupag: aggregate split allocation exceeds charge amount or 100 percent")
		}
		allocatedCents += ruleCents
	}
	return nil
}

func splitPercentageCents(amountCents int64, valueBPS int) int64 {
	bps := int64(valueBPS)
	// Dividing first preserves the backend's round-down rule without overflowing
	// at the maximum supported monetary amount.
	return amountCents/10_000*bps + amountCents%10_000*bps/10_000
}

func validateMetadata(metadata map[string]any) error {
	if metadata == nil {
		return nil
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return errors.New("mupag: metadata must be valid JSON")
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return errors.New("mupag: metadata must be valid JSON")
	}
	stack := []struct {
		value any
		depth int
	}{{decoded, 0}}
	nodes := 0
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		nodes++
		if nodes > 10_000 || current.depth > 32 {
			return errors.New("mupag: metadata exceeds complexity limit")
		}
		switch value := current.value.(type) {
		case map[string]any:
			for key, child := range value {
				normalized := strings.NewReplacer("-", "_", " ", "_").Replace(strings.ToLower(key))
				switch normalized {
				case "__proto__", "prototype", "constructor", "pan", "cvv", "cvc", "card_number", "security_code":
					return errors.New("mupag: metadata contains forbidden sensitive field")
				}
				stack = append(stack, struct {
					value any
					depth int
				}{child, current.depth + 1})
			}
		case []any:
			for _, child := range value {
				stack = append(stack, struct {
					value any
					depth int
				}{child, current.depth + 1})
			}
		}
	}
	return nil
}

func invalidText(value string, maximum int) bool {
	return strings.TrimSpace(value) == "" || len(value) > maximum || strings.ContainsAny(value, "\r\n\x00")
}

func validEmail(value string) bool {
	if strings.ContainsAny(value, " \t\r\n") || strings.Count(value, "@") != 1 {
		return false
	}
	parts := strings.SplitN(value, "@", 2)
	return parts[0] != "" && strings.Contains(parts[1], ".") && !strings.HasPrefix(parts[1], ".") && !strings.HasSuffix(parts[1], ".")
}

func validTaxID(value string) bool {
	if len(value) != 11 && len(value) != 14 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func visibleASCII(value string, allowSpace bool) bool {
	for _, character := range []byte(value) {
		minimum := byte(0x21)
		if allowSpace {
			minimum = 0x20
		}
		if character < minimum || character > 0x7e {
			return false
		}
	}
	return true
}

// List consulta uma página bounded de cobranças no escopo do merchant autenticado.
func (service *ChargesService) List(ctx context.Context, params ChargeListParams) (*ChargePage, error) {
	if err := validateChargeListParams(params); err != nil {
		return nil, err
	}
	query := url.Values{}
	if params.Status != "" {
		query.Set("status", params.Status)
	}
	if params.CustomerID != "" {
		query.Set("customer_id", params.CustomerID)
	}
	if params.PaymentMethod != "" {
		query.Set("payment_method", params.PaymentMethod)
	}
	if params.CreatedAtFrom != nil {
		query.Set("created_at_from", params.CreatedAtFrom.UTC().Format(time.RFC3339Nano))
	}
	if params.CreatedAtTo != nil {
		query.Set("created_at_to", params.CreatedAtTo.UTC().Format(time.RFC3339Nano))
	}
	if params.Limit > 0 {
		query.Set("limit", strconv.Itoa(params.Limit))
	}
	if params.Cursor != "" {
		query.Set("cursor", params.Cursor)
	}

	expectedLimit := params.Limit
	if expectedLimit == 0 {
		expectedLimit = 25
	}
	response := chargeListResponse{expectedStatus: params.Status, expectedLimit: expectedLimit}
	if params.CreatedAtFrom != nil {
		response.expectedCreatedAtFrom = params.CreatedAtFrom.UTC()
		response.correlateCreatedAtFrom = true
	}
	if params.CreatedAtTo != nil {
		response.expectedCreatedAtTo = params.CreatedAtTo.UTC()
		response.correlateCreatedAtTo = true
	}
	if err := service.client.do(ctx, http.MethodGet, "/v1/charges", query, nil, &response); err != nil {
		return nil, err
	}
	return response.page(), nil
}

func validateChargeListParams(params ChargeListParams) error {
	if params.Status != "" {
		valid := map[string]struct{}{
			"created": {}, "pending": {}, "authorized": {}, "under_review": {}, "paid": {},
			"partially_refunded": {}, "refunded": {}, "failed": {}, "expired": {},
			"cancelled": {}, "disputed": {}, "chargeback": {},
		}
		if _, ok := valid[params.Status]; !ok {
			return errors.New("mupag: invalid charge status filter")
		}
	}
	if params.CustomerID != "" && !validResourceID(params.CustomerID) {
		return errors.New("mupag: invalid customer id")
	}
	if params.PaymentMethod != "" && params.PaymentMethod != "pix" && params.PaymentMethod != "credit_card" {
		return errors.New("mupag: invalid payment method filter")
	}
	if params.Limit < 0 || params.Limit > 100 {
		return errors.New("mupag: charge list limit must be between 1 and 100")
	}
	if params.Cursor != "" {
		if err := validateOpaqueCursor(params.Cursor); err != nil {
			return err
		}
	}
	if params.CreatedAtFrom != nil && params.CreatedAtTo != nil && !params.CreatedAtFrom.Before(*params.CreatedAtTo) {
		return errors.New("mupag: created_at_from must be before created_at_to")
	}
	return nil
}
