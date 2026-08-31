package mupag_test

import (
	"testing"

	mupag "github.com/mupaybr/mupag-sdk-go"
)

func TestPublicPackageUsesMuPagName(t *testing.T) {
	client := mupag.NewClient(mupag.WithAPIKey("sk_test_123"), mupag.WithTestEnvironment())

	if client == nil {
		t.Fatal("NewClient returned nil")
	}
}
