package main

import (
	"fmt"
	"os"

	mupaysdk "github.com/mupaybr/mupay/sdks/go"
)

func main() {
	payload := []byte(os.Getenv("MUPAY_WEBHOOK_PAYLOAD"))
	signature := os.Getenv("MUPAY_WEBHOOK_SIGNATURE")
	secret := os.Getenv("MUPAY_WEBHOOK_SECRET")

	event, err := mupaysdk.Webhooks.ConstructEvent(payload, signature, secret)
	if err != nil {
		panic(err)
	}

	fmt.Println(event.Type)
}
