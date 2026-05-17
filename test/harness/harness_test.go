//go:build p1

package harness

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestP1_ListResources(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.ListResources(t.Context(), &mcp.ListResourcesParams{})
	require.NoError(t, err)
	require.Len(t, res.Resources, 1)
	require.Equal(t, "Repo conventions", res.Resources[0].Name)
	require.Equal(t, "repo:CLAUDE.md", res.Resources[0].URI)
}

func TestP1_ReadResource(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "repo:CLAUDE.md"})
	require.NoError(t, err)
	require.Len(t, res.Contents, 1)
	require.Contains(t, res.Contents[0].Text, "tiny fixture")
	require.Equal(t, "text/markdown", res.Contents[0].MIMEType)
}

func TestP1_ReadResource_Unknown(t *testing.T) {
	sess := startServer(t, "tiny")
	_, err := sess.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "repo:nonexistent.md"})
	require.Error(t, err)
}

func TestP1_GoDoc(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "go_doc",
		Arguments: map[string]any{"path": "fmt.Println"},
	})
	require.NoError(t, err)
	require.Contains(t, textOf(res), "Println")
}

func TestP1_GoListModules(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "go_list_modules",
	})
	require.NoError(t, err)
	require.Contains(t, textOf(res), "example.com/tiny")
}

func TestP1_RunTest_PassingFilter(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "run_test",
		Arguments: map[string]any{
			"run":      "TestLeaf_Passes",
			"packages": "./pkg/leaf",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "expected success: %s", textOf(res))
	out := textOf(res)
	require.Contains(t, out, "ok")
	require.NotContains(t, out, "FAIL")
}

func TestP1_RunTest_SurfacesFailures(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "run_test",
		Arguments: map[string]any{
			"run":      "TestLeaf_Fails",
			"packages": "./pkg/leaf",
			"verbose":  true,
		},
	})
	require.NoError(t, err)
	// Non-zero go-test exit is reported through ExitCode in the structured
	// payload, not as a transport error. Both the FAIL marker and the
	// intentional message should round-trip.
	out := textOf(res)
	require.Contains(t, out, "FAIL")
	require.Contains(t, out, "intentional failure")
}
