//go:build p2

package harness

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestP2_FindSymbol_Func(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "find_symbol",
		Arguments: map[string]any{"name": "Foo"},
	})
	require.NoError(t, err)

	out := unmarshalStructured[findSymbolResult](t, res)
	require.Len(t, out.Hits, 1)
	require.Equal(t, "example.com/tiny.Foo", out.Hits[0].QName)
	require.Equal(t, "func", out.Hits[0].Kind)
	require.Equal(t, "example.com/tiny", out.Hits[0].PkgPath)
}

func TestP2_FindSymbol_Method(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "find_symbol",
		Arguments: map[string]any{"name": "A", "kind": "method"},
	})
	require.NoError(t, err)

	out := unmarshalStructured[findSymbolResult](t, res)
	require.Len(t, out.Hits, 1)
	require.Equal(t, "(*example.com/tiny.S).A", out.Hits[0].QName)
	require.Equal(t, "method", out.Hits[0].Kind)
}

func TestP2_FindSymbol_Wildcard(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "find_symbol",
		Arguments: map[string]any{"name": "L*"},
	})
	require.NoError(t, err)

	out := unmarshalStructured[findSymbolResult](t, res)
	// "Leaf" in pkg/leaf matches "L*".
	require.True(t, containsQName(out.Hits, "example.com/tiny/pkg/leaf.Leaf"),
		"expected pkg/leaf.Leaf in hits, got: %+v", out.Hits)
}

func TestP2_Definition(t *testing.T) {
	sess := startServer(t, "tiny")
	// main.go line 12: `	fmt.Println(Foo())` — the "F" in Foo is at col ~14.
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "definition",
		Arguments: map[string]any{
			"file": "main.go",
			"line": 12,
			"col":  14,
		},
	})
	require.NoError(t, err)

	out := unmarshalStructured[definitionResult](t, res)
	require.Equal(t, "example.com/tiny.Foo", out.QName)
	require.Equal(t, "func", out.Kind)
	require.Equal(t, 17, out.Line) // declaration line of Foo() in main.go
}

func TestP2_References(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "references",
		Arguments: map[string]any{"qname": "example.com/tiny.Foo"},
	})
	require.NoError(t, err)

	out := unmarshalStructured[referencesResult](t, res)
	// Foo is called once, on line 12 of main.go.
	require.Len(t, out.Refs, 1)
	require.Equal(t, 12, out.Refs[0].Line)
}

func TestP2_Implementations(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "implementations",
		Arguments: map[string]any{"iface": "example.com/tiny.I"},
	})
	require.NoError(t, err)

	out := unmarshalStructured[implementationsResult](t, res)
	require.True(t, containsQName(out.Types, "example.com/tiny.S"),
		"expected example.com/tiny.S in implementers, got: %+v", out.Types)
}

func TestP2_AstGrep_Call_fmtPrintln(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "ast_grep",
		Arguments: map[string]any{
			"kind": "call",
			"func": "fmt.Println",
		},
	})
	require.NoError(t, err)

	out := unmarshalStructured[astGrepResult](t, res)
	// main.go calls fmt.Println twice (lines 12 and 13).
	require.Len(t, out.Hits, 2)
	for _, h := range out.Hits {
		require.Equal(t, "fmt.Println", h.QName)
	}
}

func TestP2_AstGrep_Implements_I(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "ast_grep",
		Arguments: map[string]any{
			"kind":  "implements",
			"iface": "example.com/tiny.I",
		},
	})
	require.NoError(t, err)

	out := unmarshalStructured[astGrepResult](t, res)
	require.True(t, containsQName(out.Hits, "example.com/tiny.S"),
		"expected example.com/tiny.S in implementers, got: %+v", out.Hits)
}
