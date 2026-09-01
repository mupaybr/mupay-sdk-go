package mupag

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const maxMoneyCents int64 = 9_000_000_000_000_000

// RefundsService agrupa solicitação e reconciliação de estornos da API pública.
type RefundsService struct {
	client *Client
}

// RefundCreateParams exige uma intenção inequívoca: AmountCents ou Full=true.
type RefundCreateParams struct {
	AmountCents *int64 `json:"amount_cents,omitempty"`
	Full        bool   `json:"full,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// Refund representa o estado retornado pelos endpoints públicos de estorno.
type Refund struct {
	RefundID      string     `json:"refund_id"`
	ChargeID      string     `json:"charge_id"`
	AmountCents   int64      `json:"amount_cents"`
	Status        string     `json:"status"`
	PSPRefundID   string     `json:"psp_refund_id,omitempty"`
	Reason        string     `json:"reason,omitempty"`
	RequestedAt   *time.Time `json:"requested_at,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	FailureReason string     `json:"failure_reason,omitempty"`
}

func (refund *Refund) validateResponse() error {
	if refund == nil || !validResourceID(refund.RefundID) || !validResourceID(refund.ChargeID) {
		return errors.New("mupag: API returned an invalid refund identity")
	}
	if refund.AmountCents < 1 || refund.AmountCents > maxMoneyCents {
		return errors.New("mupag: API returned an invalid refund amount")
	}
	if !validRefundStatus(refund.Status) {
		return errors.New("mupag: API returned an invalid refund status")
	}
	return nil
}

func validRefundStatus(status string) bool {
	switch status {
	case "requested", "processing", "completed", "failed", "cancelled", "unknown":
		return true
	default:
		return false
	}
}

type refundCreateResponse struct {
	Refund
	expectedChargeID    string
	expectedAmountCents int64
	correlateAmount     bool
}

func (response *refundCreateResponse) validateResponse() error {
	if err := response.Refund.validateResponse(); err != nil {
		return err
	}
	if response.ChargeID != response.expectedChargeID {
		return errors.New("mupag: API returned a refund for a different charge")
	}
	if response.correlateAmount && response.AmountCents != response.expectedAmountCents {
		return errors.New("mupag: API returned a refund amount that does not match the request")
	}
	return nil
}

type refundGetResponse struct {
	Refund
	expectedRefundID string
}

func (response *refundGetResponse) validateResponse() error {
	if err := response.Refund.validateResponse(); err != nil {
		return err
	}
	if response.RefundID != response.expectedRefundID {
		return errors.New("mupag: API returned a different refund than requested")
	}
	return nil
}

// RefundListParams limita paginação keyset no endpoint de reconciliação.
type RefundListParams struct {
	Limit  int
	Cursor string
}

// RefundPage preserva o cursor opaco devolvido pelo backend.
type RefundPage struct {
	Refunds    []Refund `json:"refunds"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

func (page *RefundPage) validateResponse() error {
	if page == nil {
		return errors.New("mupag: API returned an invalid refund page")
	}
	if len(page.Refunds) > 100 {
		return errors.New("mupag: API returned more than 100 refunds in one page")
	}
	if page.NextCursor != "" {
		if err := validateOpaqueCursor(page.NextCursor); err != nil {
			return errors.New("mupag: API returned invalid next_cursor")
		}
	}
	for index := range page.Refunds {
		if err := page.Refunds[index].validateResponse(); err != nil {
			return err
		}
	}
	return nil
}

type refundListResponse struct {
	Refunds          *[]Refund `json:"refunds"`
	NextCursor       string    `json:"next_cursor,omitempty"`
	expectedChargeID string
	expectedLimit    int
}

func (response *refundListResponse) validateResponse() error {
	if response == nil || response.Refunds == nil {
		return errors.New("mupag: API returned an invalid refund page")
	}
	page := response.page()
	if err := page.validateResponse(); err != nil {
		return err
	}
	if len(page.Refunds) > response.expectedLimit {
		return errors.New("mupag: API returned more refunds than the requested page limit")
	}
	for index := range page.Refunds {
		if page.Refunds[index].ChargeID != response.expectedChargeID {
			return errors.New("mupag: API returned a refund for a different charge")
		}
	}
	return nil
}

func (response *refundListResponse) page() *RefundPage {
	return &RefundPage{
		Refunds:    *response.Refunds,
		NextCursor: response.NextCursor,
	}
}

// Create solicita estorno parcial ou total com Idempotency-Key estável.
func (service *RefundsService) Create(ctx context.Context, chargeID string, params RefundCreateParams, opts ...RequestOption) (*Refund, error) {
	if !validResourceID(chargeID) {
		return nil, errors.New("mupag: invalid charge id")
	}
	hasAmount := params.AmountCents != nil
	if hasAmount == params.Full {
		return nil, errors.New("mupag: refund requires exactly one of amount_cents or full=true")
	}
	if hasAmount && (*params.AmountCents < 1 || *params.AmountCents > maxMoneyCents) {
		return nil, errors.New("mupag: invalid refund amount")
	}
	if len(params.Reason) > 500 {
		return nil, errors.New("mupag: refund reason exceeds 500 bytes")
	}

	response := refundCreateResponse{
		expectedChargeID: chargeID,
		correlateAmount:  hasAmount,
	}
	if hasAmount {
		response.expectedAmountCents = *params.AmountCents
	}
	path := "/v1/charges/" + url.PathEscape(chargeID) + "/refunds"
	if err := service.client.do(ctx, http.MethodPost, path, nil, params, &response, opts...); err != nil {
		return nil, err
	}
	return &response.Refund, nil
}

// Get consulta um estorno no escopo do merchant autenticado.
func (service *RefundsService) Get(ctx context.Context, refundID string) (*Refund, error) {
	if !validResourceID(refundID) {
		return nil, errors.New("mupag: invalid refund id")
	}
	response := refundGetResponse{expectedRefundID: refundID}
	path := "/v1/refunds/" + url.PathEscape(refundID)
	if err := service.client.do(ctx, http.MethodGet, path, nil, nil, &response); err != nil {
		return nil, err
	}
	return &response.Refund, nil
}

// ListByCharge lista estornos com limite e cursor opaco bounded.
func (service *RefundsService) ListByCharge(ctx context.Context, chargeID string, params RefundListParams) (*RefundPage, error) {
	if !validResourceID(chargeID) {
		return nil, errors.New("mupag: invalid charge id")
	}
	if params.Limit < 0 || params.Limit > 100 {
		return nil, errors.New("mupag: refund list limit must be between 1 and 100")
	}
	if params.Cursor != "" {
		if err := validateOpaqueCursor(params.Cursor); err != nil {
			return nil, err
		}
	}

	query := url.Values{}
	if params.Limit > 0 {
		query.Set("limit", strconv.Itoa(params.Limit))
	}
	if params.Cursor != "" {
		query.Set("cursor", params.Cursor)
	}
	expectedLimit := params.Limit
	if expectedLimit == 0 {
		expectedLimit = 100
	}
	response := refundListResponse{expectedChargeID: chargeID, expectedLimit: expectedLimit}
	path := "/v1/charges/" + url.PathEscape(chargeID) + "/refunds"
	if err := service.client.do(ctx, http.MethodGet, path, query, nil, &response); err != nil {
		return nil, err
	}
	return response.page(), nil
}

func validateOpaqueCursor(cursor string) error {
	if len(cursor) < 1 || len(cursor) > 256 {
		return errors.New("mupag: invalid pagination cursor")
	}
	for _, character := range []byte(cursor) {
		if (character < 'A' || character > 'Z') &&
			(character < 'a' || character > 'z') &&
			(character < '0' || character > '9') &&
			character != '_' && character != '-' {
			return errors.New("mupag: invalid pagination cursor")
		}
	}
	if _, err := base64.RawURLEncoding.Strict().DecodeString(cursor); err != nil {
		return errors.New("mupag: invalid pagination cursor")
	}
	return nil
}
