//go:build p4

package harness

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type citeResolveOut struct {
	Pattern     string `json:"pattern"`
	VendorRoot  string `json:"vendor_root"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	LineText    string `json:"line_text"`
	Context     string `json:"context,omitempty"`
	ContextLine int    `json:"context_line,omitempty"`
}

type protoFieldHit struct {
	ProtoMessage string `json:"proto_message"`
	ProtoField   string `json:"proto_field"`
	GoField      string `json:"go_field"`
	PkgPath      string `json:"pkg_path"`
	StructQN     string `json:"struct_qname"`
	FieldQN      string `json:"field_qname"`
}

type protoFieldRefHit struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
}

type protoFieldXRefOut struct {
	Field     protoFieldHit      `json:"field"`
	Refs      []protoFieldRefHit `json:"refs"`
	Truncated bool               `json:"truncated,omitempty"`
}

type lintDiagHit struct {
	Analyzer string `json:"analyzer"`
	Severity string `json:"severity"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	Message  string `json:"message"`
	Category string `json:"category,omitempty"`
}

type lintOut struct {
	Diagnostics []lintDiagHit `json:"diagnostics"`
}

func unmarshalP4[T any](t *testing.T, res *mcp.CallToolResult) T {
	t.Helper()
	require.NotNil(t, res.StructuredContent, "tool returned no structured content; text was: %s", textOf(res))
	data, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	var out T
	require.NoError(t, json.Unmarshal(data, &out))
	return out
}
