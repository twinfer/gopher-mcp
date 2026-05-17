//go:build p2

package harness

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// Mirror server-side output structs (without importing the server package's
// unexported types) so we can deserialize CallToolResult.StructuredContent.

type symbolHit struct {
	QName   string `json:"qname"`
	Kind    string `json:"kind"`
	PkgPath string `json:"pkg_path"`
	File    string `json:"file"`
	Line    int    `json:"line"`
}

type findSymbolResult struct {
	Hits []symbolHit `json:"hits"`
}

type definitionResult struct {
	QName string `json:"qname"`
	Kind  string `json:"kind"`
	File  string `json:"file"`
	Line  int    `json:"line"`
	Col   int    `json:"col"`
}

type referenceHit struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
}

type referencesResult struct {
	Refs      []referenceHit `json:"refs"`
	Truncated bool           `json:"truncated,omitempty"`
}

type implementationsResult struct {
	Types []symbolHit `json:"types"`
}

type astGrepHit struct {
	QName   string `json:"qname"`
	PkgPath string `json:"pkg_path"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
}

type astGrepResult struct {
	Hits []astGrepHit `json:"hits"`
}

// unmarshalStructured decodes res.StructuredContent into T.
func unmarshalStructured[T any](t *testing.T, res *mcp.CallToolResult) T {
	t.Helper()
	require.NotNil(t, res.StructuredContent, "tool returned no structured content; text was: %s", textOf(res))
	data, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	var out T
	require.NoError(t, json.Unmarshal(data, &out))
	return out
}

// containsQName reports whether any hit in s has QName == want.
func containsQName[T interface{ getQName() string }](s []T, want string) bool {
	for _, h := range s {
		if h.getQName() == want {
			return true
		}
	}
	return false
}

func (h symbolHit) getQName() string  { return h.QName }
func (h astGrepHit) getQName() string { return h.QName }
