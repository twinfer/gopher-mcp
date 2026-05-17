package bannedinscope_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/twinfer/gopher-mcp/pkg/analyzers/bannedinscope"
)

func TestBannedInScope(t *testing.T) {
	a, err := bannedinscope.Factory(map[string]any{
		"banned":         []any{"time.Now", "os.Getenv"},
		"scope_packages": []any{"insidescope"},
	})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	dir := analysistest.TestData()
	// Run on both packages; only the in-scope one should yield diagnostics
	// (and those diagnostics are checked via `// want` comments).
	analysistest.Run(t, dir, a, "insidescope", "outsidescope")
}

func TestFactory_EmptyBanned(t *testing.T) {
	if _, err := bannedinscope.Factory(map[string]any{}); err == nil {
		t.Fatal("expected error for empty banned list")
	}
}

func TestFactory_BadType(t *testing.T) {
	_, err := bannedinscope.Factory(map[string]any{
		"banned": "not-a-list",
	})
	if err == nil {
		t.Fatal("expected error for non-list 'banned'")
	}
}
