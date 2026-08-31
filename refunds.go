package mupag

import (
	"context"
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
	if refund.Status == "" || len(refund.Status) > 64 {
		return errors.New("mupag: API returned an invalid refund status")
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

	var refund Refund
	path := "/v1/charges/" + url.PathEscape(chargeID) + "/refunds"
	if err := service.client.do(ctx, http.MethodPost, path, nil, params, &refund, opts...); err != nil {
		return nil, err
	}
	return &refund, nil
}

// Get consulta um estorno no escopo do merchant autenticado.
func (service *RefundsService) Get(ctx context.Context, refundID string) (*Refund, error) {
	if !validResourceID(refundID) {
		return nil, errors.New("mupag: invalid refund id")
	}
	var refund Refund
	path := "/v1/refunds/" + url.PathEscape(refundID)
	if err := service.client.do(ctx, http.MethodGet, path, nil, nil, &refund); err != nil {
		return nil, err
	}
	return &refund, nil
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
	var page RefundPage
	path := "/v1/charges/" + url.PathEscape(chargeID) + "/refunds"
	if err := service.client.do(ctx, http.MethodGet, path, query, nil, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

func validateOpaqueCursor(cursor string) error {
	if len(cursor) < 1 || len(cursor) > 256 {
		return errors.New("mupag: invalid pagination cursor")
	}
	for _, character := range []byte(cursor) {
		if character < 0x21 || character > 0x7e {
			return errors.New("mupag: invalid pagination cursor")
		}
	}
	return nil
}
