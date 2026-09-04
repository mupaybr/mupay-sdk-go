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

	subscription, err := client.Subscriptions.Cancel(
		context.Background(),
		"sub_123",
		mupag.CancelSubscriptionParams{
			Mode:   "immediate",
			Reason: "pedido do cliente",
		},
		mupag.WithIdempotencyKey("cancel_sub_123"),
	)
	if err != nil {
		panic(err)
	}

	fmt.Println(subscription.Status)
}
