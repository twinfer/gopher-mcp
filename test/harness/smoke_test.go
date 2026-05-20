//go:build smoke

package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// TestSmoke_TinyBinary verifies the built gopher-mcp binary works as a real
// subprocess over stdio. The in-process P1-P5 harness covers tool semantics
// exhaustively; this test exists only to catch wiring regressions in the
// CLI/stdio path that wouldn't surface in-process — bad flag handling,
// transport setup, server shutdown.
//
// Run with: go test -tags=smoke -count=1 ./test/harness/...
func TestSmoke_TinyBinary(t *testing.T) {
	binDir := t.TempDir()
	binary := filepath.Join(binDir, "gopher-mcp")
	build := exec.Command("go", "build", "-o", binary, "github.com/twinfer/gopher-mcp/cmd/gopher-mcp")
	build.Stderr = os.Stderr
	require.NoError(t, build.Run(), "building gopher-mcp binary")

	fixture, err := filepath.Abs(filepath.Join("..", "fixtures", "tiny"))
	require.NoError(t, err)
	cmd := exec.Command(binary, "-root", fixture)
	cmd.Stderr = os.Stderr

	cli := mcp.NewClient(&mcp.Implementation{Name: "gopher-mcp-smoke"}, nil)
	sess, err := cli.Connect(t.Context(), &mcp.CommandTransport{Command: cmd}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })

	t.Run("ListTools", func(t *testing.T) {
		res, err := sess.ListTools(t.Context(), &mcp.ListToolsParams{})
		require.NoError(t, err)
		names := make(map[string]bool, len(res.Tools))
		for _, tl := range res.Tools {
			names[tl.Name] = true
		}
		for _, want := range []string{"find_symbol", "definition", "references", "ast_grep", "callers", "go_doc"} {
			require.True(t, names[want], "missing tool %q; got %v", want, names)
		}
	})

	t.Run("FindSymbol_Foo", func(t *testing.T) {
		res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "find_symbol",
			Arguments: map[string]any{"name": "Foo"},
		})
		require.NoError(t, err)
		require.False(t, res.IsError, "find_symbol errored: %s", textOf(res))
		require.Contains(t, textOf(res), "1 symbol",
			"expected single Foo hit in tiny fixture, got: %s", textOf(res))
	})

	t.Run("FindSymbol_ScopeFlag_RoundTrip", func(t *testing.T) {
		// Smoke that the scope field deserializes correctly over stdio.
		res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "find_symbol",
			Arguments: map[string]any{"name": "Foo", "scope": "workspace"},
		})
		require.NoError(t, err)
		require.False(t, res.IsError, "find_symbol with scope errored: %s", textOf(res))
	})
}
