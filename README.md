# MuPag SDK for Go

SDK oficial Go da MuPag para criar cobrancas PIX/cartao, cancelar assinaturas e validar webhooks sem montar HTTP na mao.

Requisito de seguranca: Go 1.26.5 ou superior. Versoes anteriores da biblioteca padrao
possuem vulnerabilidades alcancaveis nos fluxos TLS/HTTP usados pelo cliente.

Instalacao:

```bash
go get github.com/mupaybr/mupag-sdk-go@v0.2.0
```

Uso recomendado no codigo:

```go
import mupag "github.com/mupaybr/mupag-sdk-go"
```

## Primeira cobranca em poucos minutos

```go
package main

import (
	"context"
	"fmt"
	"os"

	mupag "github.com/mupaybr/mupag-sdk-go"
)

func main() {
	client := mupag.NewClient(
		mupag.WithAPIKey(os.Getenv("MUPAG_API_KEY")),
		mupag.WithTestEnvironment(),
	)

	charge, err := client.Charges.Create(
		context.Background(),
		mupag.ChargeCreateParams{
			AmountCents:   9900,
			PaymentMethod: "pix",
			Customer: mupag.CustomerParams{
				Name:  "Ana Silva",
				Email: "ana@example.test",
				TaxID: "12345678901",
			},
			Description:       "Pedido #123",
			ExternalReference: "pedido-123",
			ExpiresInSeconds:  3600,
		},
		mupag.WithIdempotencyKey("order_123_charge_1"),
	)
	if err != nil {
		panic(err)
	}

	fmt.Println("Charge:", charge.ChargeID)
	fmt.Println("PIX copia e cola:", charge.PixEMVCode)
}
```

Fluxo mental simples:

1. Crie `client` com `MUPAG_API_KEY`.
2. Chame `client.Charges.Create`.
3. Mostre `PixEMVCode` ou `PixQRCodeBase64` para o cliente pagar.
4. Valide webhook com `mupag.Webhooks.ConstructEvent`.

## Cartao com token Asaas

A MuPag nunca deve receber PAN/CVV. Gere `card_token` no iFrame Asaas e envie so o token:

```go
charge, err := client.Charges.Create(ctx, mupag.ChargeCreateParams{
	AmountCents:   50000,
	PaymentMethod: "credit_card",
	Installments:  1,
	CardToken:     "tok_abc123",
	SaveCard:      true,
	PayerIP:       "203.0.113.10",
	Customer: mupag.CustomerParams{
		ID:    "22222222-2222-2222-2222-222222222222",
		Name:  "Ana Silva",
		Email: "ana@example.test",
		TaxID: "12345678901",
	},
})
```

O contrato atual aceita apenas uma parcela, exige o IP literal do pagador e rejeita
`SoftDescriptor`. Comerciantes que exigem 3DS devem aguardar o suporte oficial antes de ativar cartão.

## Idempotencia sem atrito

POSTs geram `Idempotency-Key` automaticamente e reutilizam essa chave nos retries internos da
mesma invocacao. Uma nova chamada do seu processo gera outra chave, portanto operacoes financeiras
devem usar uma chave de negocio estavel desde a primeira tentativa:

```go
charge, err := client.Charges.Create(
	ctx,
	params,
	mupag.WithIdempotencyKey("order_123_charge_1"),
)
```

Mesma chave + mesmo payload retorna a resposta anterior. Mesma chave + payload diferente deve retornar erro de validacao.

Se a API puder ter aceitado a mutacao mas a resposta se perder, o SDK retorna
`*mupag.OutcomeUnknownError`. O campo `IdempotencyKey` e exatamente a chave enviada. Persista-a e
reconcilie ou repita **o mesmo payload** com `WithIdempotencyKey`; nao crie uma nova operacao.

O SDK repete de forma limitada falhas de transporte, `408`, `425`, `429`, `5xx` e
`409/idempotency_in_progress`, sempre com a mesma chave, backoff exponencial com jitter e
`Retry-After` limitado. `409/idempotency_outcome_unknown` e desconhecido imediatamente;
`fingerprint_conflict` e demais `4xx` nao classificados como ambiguos sao definitivos apenas quando
nenhuma tentativa anterior ficou ambigua. Depois de uma ambiguidade, somente um `2xx` JSON estrutural e financeiramente valido confirma
o resultado; outro `4xx`, `409` ou `429` nao apaga o estado desconhecido.

## Cancelar assinatura

O endpoint real exige `mode`; `reason` e opcional:

```go
subscription, err := client.Subscriptions.Cancel(
	ctx,
	"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
	mupag.CancelSubscriptionParams{
		Mode:   "immediate",
		Reason: "pedido do cliente",
	},
	mupag.WithIdempotencyKey("cancel_sub_123"),
)
```

## Webhooks HMAC

Valide payload bruto antes de confiar em qualquer campo:

```go
event, err := mupag.Webhooks.ConstructEvent(
	rawBody,
	r.Header.Get("MuPag-Signature"),
	os.Getenv("MUPAG_WEBHOOK_SECRET"),
)
if err != nil {
	http.Error(w, "invalid signature", http.StatusUnauthorized)
	return
}

fmt.Println(event.Type)
```

A assinatura usa HMAC-SHA256 sobre `{timestamp}.{raw_json_body}` com tolerancia padrao de 5 minutos e comparacao em tempo constante. O JSON validado segue `{ "id": string, "type": string, "data": object }`.

## Erros tipados

```go
charge, err := client.Charges.Create(ctx, params)
if err != nil {
	var outcomeErr *mupag.OutcomeUnknownError
	if errors.As(err, &outcomeErr) {
		persistForReconciliation(outcomeErr.IdempotencyKey, params)
		return
	}

	var rateErr *mupag.RateLimitError
	if errors.As(err, &rateErr) {
		time.Sleep(rateErr.RetryAfter)
		return
	}

	var apiErr *mupag.APIError
	if errors.As(err, &apiErr) {
		log.Printf("mupag error code=%s request_id=%s", apiErr.Code, apiErr.RequestID)
		return
	}
}
_ = charge
```

## Examples

- `examples/create_charge`
- `examples/cancel_subscription`
- `examples/verify_webhook`

Todos compilam com `go test ./...`.

## Desenvolvimento

```bash
cd sdks/go
go test ./...
go test -coverprofile coverage.out .
go vet ./...
go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.62.2 run
```

O SDK nao tem dependencias de runtime externas. Menos supply chain, menos coisa para quebrar no projeto do lojista.

## Publicacao

O caminho canonico do modulo e `github.com/mupaybr/mupag-sdk-go`; instale a versao publicada com o comando acima. Referencias oficiais: [Publishing a module](https://go.dev/doc/modules/publishing), [Managing module source](https://go.dev/doc/modules/managing-source) e [Go Modules Reference](https://go.dev/ref/mod).

## Migracao

O repositorio historico dedicado `mupaybr/mupay-sdk-go` e o caminho de modulo anterior `github.com/mupaybr/mupay/sdks/go` nao sao mais suportados. Atualize os imports para `github.com/mupaybr/mupag-sdk-go` e mantenha o alias de pacote `mupag`.
