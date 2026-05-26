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

	subscription, err := client.Subscriptions.Cancel(
		context.Background(),
		"sub_123",
		mupaysdk.CancelSubscriptionParams{
			Mode:   "immediate",
			Reason: "pedido do cliente",
		},
		mupaysdk.WithIdempotencyKey("cancel_sub_123"),
	)
	if err != nil {
		panic(err)
	}

	fmt.Println(subscription.Status)
}
