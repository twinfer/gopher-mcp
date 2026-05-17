//go:build p3

package harness

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type callEdge struct {
	CallerQN string `json:"caller_qname"`
	CalleeQN string `json:"callee_qname"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Col      int    `json:"col,omitempty"`
}

type callersResult struct {
	Edges []callEdge `json:"edges"`
}

type calleesResult struct {
	Edges []callEdge `json:"edges"`
}

type reverseTraceResult struct {
	Path  []callEdge `json:"path"`
	Found bool       `json:"found"`
}

// unmarshalP3 is a P3-local copy of unmarshalStructured so this file compiles
// without the p2 build tag.
func unmarshalP3[T any](t *testing.T, res *mcp.CallToolResult) T {
	t.Helper()
	require.NotNil(t, res.StructuredContent, "tool returned no structured content; text was: %s", textOf(res))
	data, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	var out T
	require.NoError(t, json.Unmarshal(data, &out))
	return out
}

func hasCallee(edges []callEdge, callee string) bool {
	for _, e := range edges {
		if e.CalleeQN == callee {
			return true
		}
	}
	return false
}

func hasCaller(edges []callEdge, caller string) bool {
	for _, e := range edges {
		if e.CallerQN == caller {
			return true
		}
	}
	return false
}
