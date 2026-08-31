package main

import (
	"fmt"
	"os"

	mupag "github.com/mupaybr/mupag-sdk-go"
)

func main() {
	payload := []byte(os.Getenv("MUPAG_WEBHOOK_PAYLOAD"))
	signature := os.Getenv("MUPAG_WEBHOOK_SIGNATURE")
	secret := os.Getenv("MUPAG_WEBHOOK_SECRET")

	event, err := mupag.Webhooks.ConstructEvent(payload, signature, secret)
	if err != nil {
		panic(err)
	}

	fmt.Println(event.Type)
}
