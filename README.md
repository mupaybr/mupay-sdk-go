# mupay-sdk for Go

SDK oficial Go da Mupay para criar cobrancas PIX/cartao, cancelar assinaturas e validar webhooks sem montar HTTP na mao.

Instalacao no monorepo atual:

```bash
go get github.com/marcosvbarra/mupay/sdks/go@latest
```

Uso recomendado no codigo:

```go
import mupaysdk "github.com/marcosvbarra/mupay/sdks/go"
```

> Quer o nome publico bonito `github.com/mupay/mupay-sdk-go`? Veja [Publicacao](#publicacao). Em Go, o `module` precisa apontar para um repositorio publico que exista.

## Primeira cobranca em poucos minutos

```go
package main

import (
	"context"
	"fmt"
	"os"

	mupaysdk "github.com/marcosvbarra/mupay/sdks/go"
)

func main() {
	client := mupaysdk.NewClient(
		mupaysdk.WithAPIKey(os.Getenv("MUPAY_API_KEY")),
		mupaysdk.WithTestEnvironment(),
	)

	charge, err := client.Charges.Create(context.Background(), mupaysdk.ChargeCreateParams{
		AmountCents:   9900,
		PaymentMethod: "pix",
		Customer: mupaysdk.CustomerParams{
			Name:  "Ana Silva",
			Email: "ana@example.test",
			TaxID: "12345678901",
		},
		Description:       "Pedido #123",
		ExternalReference: "pedido-123",
		ExpiresInSeconds:  3600,
	})
	if err != nil {
		panic(err)
	}

	fmt.Println("Charge:", charge.ChargeID)
	fmt.Println("PIX copia e cola:", charge.PixEMVCode)
}
```

Fluxo mental simples:

1. Crie `client` com `MUPAY_API_KEY`.
2. Chame `client.Charges.Create`.
3. Mostre `PixEMVCode` ou `PixQRCodeBase64` para o cliente pagar.
4. Valide webhook com `mupaysdk.Webhooks.ConstructEvent`.

## Cartao com token Asaas

A Mupay nunca deve receber PAN/CVV. Gere `card_token` no iFrame Asaas e envie so o token:

```go
charge, err := client.Charges.Create(ctx, mupaysdk.ChargeCreateParams{
	AmountCents:   50000,
	PaymentMethod: "credit_card",
	Installments:  6,
	CardToken:     "tok_abc123",
	SaveCard:      true,
	Customer: mupaysdk.CustomerParams{
		ID: "22222222-2222-2222-2222-222222222222",
	},
	SoftDescriptor: "MEU PRODUTO",
})
```

Se a resposta tiver `ThreeDSACSURL`, redirecione o comprador para o desafio 3DS.

## Idempotencia sem atrito

POSTs geram `Idempotency-Key` automaticamente. Para amarrar tentativa ao seu pedido:

```go
charge, err := client.Charges.Create(
	ctx,
	params,
	mupaysdk.WithIdempotencyKey("order_123_charge_1"),
)
```

Mesma chave + mesmo payload retorna a resposta anterior. Mesma chave + payload diferente deve retornar erro de validacao.

## Cancelar assinatura

O endpoint real exige `mode`; `reason` e opcional:

```go
subscription, err := client.Subscriptions.Cancel(
	ctx,
	"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
	mupaysdk.CancelSubscriptionParams{
		Mode:   "immediate",
		Reason: "pedido do cliente",
	},
	mupaysdk.WithIdempotencyKey("cancel_sub_123"),
)
```

## Webhooks HMAC

Valide payload bruto antes de confiar em qualquer campo:

```go
event, err := mupaysdk.Webhooks.ConstructEvent(
	rawBody,
	r.Header.Get("X-Gateway-Signature"),
	os.Getenv("MUPAY_WEBHOOK_SECRET"),
)
if err != nil {
	http.Error(w, "invalid signature", http.StatusUnauthorized)
	return
}

fmt.Println(event.Type)
```

A assinatura usa HMAC-SHA256 sobre `{timestamp}.{raw_json_body}` com tolerancia padrao de 5 minutos e comparacao em tempo constante.

## Erros tipados

```go
charge, err := client.Charges.Create(ctx, params)
if err != nil {
	var rateErr *mupaysdk.RateLimitError
	if errors.As(err, &rateErr) {
		time.Sleep(rateErr.RetryAfter)
		return
	}

	var apiErr *mupaysdk.APIError
	if errors.As(err, &apiErr) {
		log.Printf("mupay error code=%s request_id=%s", apiErr.Code, apiErr.RequestID)
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

Go e descentralizado: nao existe um "npm publish". Para publicar, o module path precisa apontar para codigo publico acessivel por `go get`, e uma tag SemVer precisa existir. Referencias oficiais:

- [Publishing a module](https://go.dev/doc/modules/publishing)
- [Managing module source](https://go.dev/doc/modules/managing-source)
- [Go Modules Reference](https://go.dev/ref/mod)

### Opcao A: publicar pelo monorepo atual

Use o module path deste PR:

```go
module github.com/marcosvbarra/mupay/sdks/go
```

Release:

```bash
git tag sdks/go/v0.1.0
git push origin sdks/go/v0.1.0
GOPROXY=proxy.golang.org go list -m github.com/marcosvbarra/mupay/sdks/go@v0.1.0
```

Por ser modulo em subdiretorio, a tag precisa ter prefixo `sdks/go/`. Depois disso, `pkg.go.dev/github.com/marcosvbarra/mupay/sdks/go` indexa a documentacao.

### Opcao B: publicar com nome publico bonito

Crie um repo publico dedicado:

```text
github.com/mupay/mupay-sdk-go
```

Antes do primeiro release, troque:

```go
module github.com/mupay/mupay-sdk-go
```

E nos exemplos:

```go
import mupaysdk "github.com/mupay/mupay-sdk-go"
```

Release:

```bash
git tag v0.1.0
git push origin v0.1.0
GOPROXY=proxy.golang.org go list -m github.com/mupay/mupay-sdk-go@v0.1.0
```

Essa e a melhor experiencia publica: nome curto, repo dedicado, `go get github.com/mupay/mupay-sdk-go@latest`, docs limpas no pkg.go.dev.
