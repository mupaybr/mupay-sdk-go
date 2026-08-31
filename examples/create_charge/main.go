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

	charge, err := client.Charges.Create(context.Background(), mupag.ChargeCreateParams{
		AmountCents:   9900,
		PaymentMethod: "pix",
		Customer: mupag.CustomerParams{
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
