package mupag_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	mupag "github.com/mupaybr/mupag-sdk-go"
)

func TestPublicPackageUsesMuPagName(t *testing.T) {
	client := mupag.NewClient(mupag.WithAPIKey("sk_test_123"), mupag.WithTestEnvironment())

	if client == nil {
		t.Fatal("NewClient returned nil")
	}
}

func TestReadmeDocumentsTruthfulGoModuleMigration(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}

	contents := string(readme)
	if !strings.Contains(contents, "github.com/mupaybr/mupay/sdks/go") {
		t.Fatal("README does not name the prior Go module path")
	}
	if !strings.Contains(contents, "github.com/mupaybr/mupag-sdk-go") {
		t.Fatal("README does not name the canonical Go module path")
	}
	formerPersonalModule := strings.Join([]string{"github.com", "marcosvbarra", "mupay"}, "/")
	if strings.Contains(contents, formerPersonalModule) {
		t.Fatal("README names the former personal-owner module path")
	}
}
