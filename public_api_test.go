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

func TestReadmeMigrationConfiguresProductionAndSandboxExplicitly(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}

	migrationStart := strings.Index(string(readme), "## Migracao")
	if migrationStart == -1 {
		t.Fatal("README does not contain the migration section")
	}
	migration := string(readme[migrationStart:])
	codeStart := strings.Index(migration, "```go")
	if codeStart == -1 {
		t.Fatal("migration section does not contain a Go example")
	}
	codeStart += len("```go")
	codeEnd := strings.Index(migration[codeStart:], "```")
	if codeEnd == -1 {
		t.Fatal("migration Go example is not closed")
	}
	productionExample := migration[codeStart : codeStart+codeEnd]
	if !strings.Contains(productionExample, "mupag.NewClient(") ||
		!strings.Contains(productionExample, "mupag.WithPrdEnvironment()") {
		t.Fatal("migration example does not configure the production environment")
	}
	if strings.Contains(productionExample, "mupag.WithTestEnvironment()") {
		t.Fatal("migration production example configures the test environment")
	}
	if !strings.Contains(migration, "mupag.WithTestEnvironment()") {
		t.Fatal("migration section does not document the sandbox/test environment")
	}
}

func TestCreateChargeExampleDocumentsStableIdempotencyKey(t *testing.T) {
	example, err := os.ReadFile(filepath.Join("examples", "create_charge", "main.go"))
	if err != nil {
		t.Fatalf("read create charge example: %v", err)
	}

	contents := string(example)
	if !strings.Contains(contents, `mupag.WithIdempotencyKey("order_123_charge")`) {
		t.Fatal("create charge example does not use a stable business idempotency key")
	}
	if !strings.Contains(contents, "mesma chave em retries da mesma cobrança") {
		t.Fatal("create charge example does not explain why the idempotency key must be reused")
	}
}
