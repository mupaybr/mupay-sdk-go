package main

import (
	"context"
	"fmt"
	"os"

	mupaysdk "github.com/mupaybr/mupay/sdks/go"
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
	})
	if err != nil {
		panic(err)
	}

	fmt.Println(charge.ChargeID)
}
