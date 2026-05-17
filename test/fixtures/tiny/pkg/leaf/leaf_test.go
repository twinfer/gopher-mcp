package leaf

import "testing"

func TestLeaf_Passes(t *testing.T) {
	if got := Leaf(); got != "leaf" {
		t.Fatalf("Leaf() = %q, want %q", got, "leaf")
	}
}

func TestLeaf_Fails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped in short mode")
	}
	t.Fatal("intentional failure used by gopher-mcp run_test harness")
}
