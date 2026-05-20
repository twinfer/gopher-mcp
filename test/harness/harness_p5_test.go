//go:build p5

package harness

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/twinfer/gopher-mcp/internal/config"
)

// The `tiny` fixture imports `fmt` (stdlib) but declares no direct require
// in go.mod beyond the implicit stdlib. So the tests below toggle
// dep_index.stdlib at startup to verify that scope semantics behave the same
// across tiers — stdlib stands in for "external indexed code" here.

func enableStdlibIndex(cfg *config.RepoConfig) {
	t := true
	cfg.DepIndex = config.DepIndexConfig{Direct: &t, Stdlib: true}
}

func TestP5_FindSymbol_StdlibHidden_ByDefault(t *testing.T) {
	// Default config: stdlib NOT indexed. find_symbol "Println" → 0 hits.
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "find_symbol",
		Arguments: map[string]any{"name": "Println"},
	})
	require.NoError(t, err)
	out := unmarshalStructured[findSymbolResult](t, res)
	require.Empty(t, out.Hits, "stdlib not indexed; expected zero hits for Println")
}

func TestP5_FindSymbol_StdlibIndexed_AllScope(t *testing.T) {
	// Stdlib indexed AND scope=all → fmt.Println surfaces with Tier=stdlib.
	sess := startServerWith(t, "tiny", enableStdlibIndex)
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "find_symbol",
		Arguments: map[string]any{"name": "Println", "scope": "all"},
	})
	require.NoError(t, err)
	out := unmarshalStructured[findSymbolResult](t, res)
	require.NotEmpty(t, out.Hits, "expected fmt.Println in find_symbol results")

	var found bool
	for _, h := range out.Hits {
		if h.QName == "fmt.Println" {
			found = true
			require.Equal(t, "stdlib", h.Tier, "fmt.Println should be classified as stdlib tier")
			require.Equal(t, "func", h.Kind)
		}
	}
	require.True(t, found, "fmt.Println missing from hits: %+v", out.Hits)
}

func TestP5_FindSymbol_StdlibIndexed_DefaultScopeExcludes(t *testing.T) {
	// Stdlib indexed but default scope (workspace+direct) filters it out.
	sess := startServerWith(t, "tiny", enableStdlibIndex)
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "find_symbol",
		Arguments: map[string]any{"name": "Println"}, // no scope ⇒ default
	})
	require.NoError(t, err)
	out := unmarshalStructured[findSymbolResult](t, res)
	require.Empty(t, out.Hits, "default scope should exclude stdlib hits even when stdlib is indexed")
}

func TestP5_References_StdlibTarget_DefaultScope(t *testing.T) {
	// fmt.Println is called from tiny/main.go twice. Default scope walks
	// workspace+direct, so it should find the two workspace call sites.
	sess := startServerWith(t, "tiny", enableStdlibIndex)
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "references",
		Arguments: map[string]any{
			"qname": "fmt.Println",
		},
	})
	require.NoError(t, err)
	out := unmarshalStructured[referencesResult](t, res)
	require.GreaterOrEqual(t, len(out.Refs), 2,
		"expected at least 2 fmt.Println call sites in workspace; got %+v", out.Refs)
}

func TestP5_References_ScopeAll_BroadensIntoStdlib(t *testing.T) {
	// scope=all walks every indexed tier. fmt.Println is itself called inside
	// the stdlib (via Fprintln etc.), so scope=all should produce strictly
	// more hits than the default workspace+direct scope.
	sess := startServerWith(t, "tiny", enableStdlibIndex)
	def, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "references",
		Arguments: map[string]any{"qname": "fmt.Println"},
	})
	require.NoError(t, err)
	defOut := unmarshalStructured[referencesResult](t, def)

	all, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "references",
		Arguments: map[string]any{"qname": "fmt.Println", "scope": "all"},
	})
	require.NoError(t, err)
	allOut := unmarshalStructured[referencesResult](t, all)

	require.Greater(t, len(allOut.Refs), len(defOut.Refs),
		"scope=all should surface dep-internal references; default=%d all=%d",
		len(defOut.Refs), len(allOut.Refs))
}

func TestP5_AstGrep_FmtPrintln_WorkspaceScope(t *testing.T) {
	// ast_grep call=fmt.Println with scope=workspace should match the two
	// call sites in tiny/main.go regardless of whether stdlib is indexed
	// (the matcher walks workspace Syntax + TypesInfo, both always present).
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "ast_grep",
		Arguments: map[string]any{
			"kind":  "call",
			"func":  "fmt.Println",
			"scope": "workspace",
		},
	})
	require.NoError(t, err)
	out := unmarshalStructured[astGrepResult](t, res)
	require.GreaterOrEqual(t, len(out.Hits), 2,
		"expected fmt.Println calls in workspace tiny/main.go; got %+v", out.Hits)
	for _, h := range out.Hits {
		require.Equal(t, "fmt.Println", h.QName)
	}
}

func TestP5_ParseScope_RejectsBogus(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "find_symbol",
		Arguments: map[string]any{
			"name":  "Foo",
			"scope": "weird-scope",
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected error for unknown scope; got: %s", textOf(res))
}

func TestP5_DefaultConfig_WorkspaceTierClassification(t *testing.T) {
	// Without any DepIndex config, workspace symbols still surface with
	// Tier="workspace" in their hit metadata.
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "find_symbol",
		Arguments: map[string]any{"name": "Foo"},
	})
	require.NoError(t, err)
	out := unmarshalStructured[findSymbolResult](t, res)
	require.Len(t, out.Hits, 1)
	require.Equal(t, "workspace", out.Hits[0].Tier,
		"tiny.Foo should be classified as workspace tier")
}
