//go:build p3

package harness

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// Call structure in fixtures/tiny:
//
//	example.com/tiny.main → (*example.com/tiny.S).A, example.com/tiny.Foo,
//	                       fmt.Println, example.com/tiny/pkg/leaf.Leaf
//	(*example.com/tiny.S).A → (*example.com/tiny.S).B
//	(*example.com/tiny.S).B → example.com/tiny/pkg/leaf.Leaf
//	example.com/tiny.Foo → (nothing)
//
// CHA over a partial program will surface synthetic root edges into every
// exported function as well; we assert positive presence rather than exact
// counts.

func TestP3_Callees_Main(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "callees",
		Arguments: map[string]any{"qname": "example.com/tiny.main"},
	})
	require.NoError(t, err)

	out := unmarshalP3[calleesResult](t, res)
	require.True(t, hasCallee(out.Edges, "(*example.com/tiny.S).A"),
		"expected main → (*tiny.S).A, got: %+v", out.Edges)
	require.True(t, hasCallee(out.Edges, "example.com/tiny.Foo"),
		"expected main → tiny.Foo, got: %+v", out.Edges)
	require.True(t, hasCallee(out.Edges, "example.com/tiny/pkg/leaf.Leaf"),
		"expected main → leaf.Leaf, got: %+v", out.Edges)
}

func TestP3_Callers_LeafFunc(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "callers",
		Arguments: map[string]any{"qname": "example.com/tiny/pkg/leaf.Leaf"},
	})
	require.NoError(t, err)

	out := unmarshalP3[callersResult](t, res)
	// leaf.Leaf is called from main and from (*S).B.
	require.True(t, hasCaller(out.Edges, "example.com/tiny.main"),
		"expected main as caller of leaf.Leaf, got: %+v", out.Edges)
	require.True(t, hasCaller(out.Edges, "(*example.com/tiny.S).B"),
		"expected (*S).B as caller of leaf.Leaf, got: %+v", out.Edges)
}

func TestP3_Callees_S_A_HitsB(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "callees",
		Arguments: map[string]any{"qname": "(*example.com/tiny.S).A"},
	})
	require.NoError(t, err)

	out := unmarshalP3[calleesResult](t, res)
	require.True(t, hasCallee(out.Edges, "(*example.com/tiny.S).B"),
		"expected (*S).A → (*S).B, got: %+v", out.Edges)
}

func TestP3_ReverseTrace_MainToLeaf(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "reverse_trace",
		Arguments: map[string]any{
			"target":       "example.com/tiny/pkg/leaf.Leaf",
			"entry_points": []string{"example.com/tiny.main"},
		},
	})
	require.NoError(t, err)

	out := unmarshalP3[reverseTraceResult](t, res)
	require.True(t, out.Found, "expected a path from main to leaf.Leaf, got: %+v", out)
	require.NotEmpty(t, out.Path)
	require.Equal(t, "example.com/tiny.main", out.Path[0].CallerQN,
		"path should start at main; got: %+v", out.Path)
	last := out.Path[len(out.Path)-1]
	require.Equal(t, "example.com/tiny/pkg/leaf.Leaf", last.CalleeQN,
		"path should end at leaf.Leaf; got: %+v", out.Path)
}

func TestP3_ReverseTrace_NoPath(t *testing.T) {
	sess := startServer(t, "tiny")
	// Foo doesn't call anything; there's no path from Foo to leaf.Leaf.
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "reverse_trace",
		Arguments: map[string]any{
			"target":       "example.com/tiny/pkg/leaf.Leaf",
			"entry_points": []string{"example.com/tiny.Foo"},
		},
	})
	require.NoError(t, err)

	out := unmarshalP3[reverseTraceResult](t, res)
	require.False(t, out.Found, "expected no path from Foo to leaf.Leaf")
	require.Empty(t, out.Path)
}

func TestP3_Precision_RTA_Callees(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "callees",
		Arguments: map[string]any{
			"qname":        "example.com/tiny.main",
			"precision":    "rta",
			"entry_points": []string{"example.com/tiny.main"},
		},
	})
	require.NoError(t, err)

	out := unmarshalP3[calleesResult](t, res)
	require.True(t, hasCallee(out.Edges, "(*example.com/tiny.S).A"),
		"RTA: expected main → (*tiny.S).A, got: %+v", out.Edges)
	require.True(t, hasCallee(out.Edges, "example.com/tiny.Foo"),
		"RTA: expected main → tiny.Foo, got: %+v", out.Edges)
}

func TestP3_BadPrecision(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "callers",
		Arguments: map[string]any{
			"qname":     "example.com/tiny.main",
			"precision": "bogus",
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected IsError for bogus precision; text was: %s", textOf(res))
}

func TestP3_UnknownFunction(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "callers",
		Arguments: map[string]any{"qname": "example.com/tiny.DoesNotExist"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected IsError for unknown function; text was: %s", textOf(res))
}
