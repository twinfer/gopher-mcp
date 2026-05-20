package harness

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/twinfer/gopher-mcp/internal/config"
	"github.com/twinfer/gopher-mcp/internal/index"
	"github.com/twinfer/gopher-mcp/internal/server"
)

// startServer wires an in-process MCP client/server pair against a fixture
// under test/fixtures. The Index is loaded synchronously before any test runs.
func startServer(t *testing.T, fixture string) *mcp.ClientSession {
	t.Helper()
	return startServerWith(t, fixture, nil)
}

// startServerWith is like startServer but lets a caller mutate the loaded
// RepoConfig (e.g. to flip dep_index tiers on/off) before the index is built.
func startServerWith(t *testing.T, fixture string, mutate func(*config.RepoConfig)) *mcp.ClientSession {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "fixtures", fixture))
	require.NoError(t, err)
	cfg, _, err := config.Load(root)
	require.NoError(t, err)
	if mutate != nil {
		mutate(&cfg)
	}

	ix := index.New(root, cfg, nil)
	require.NoError(t, ix.Reload(t.Context()))

	srv := server.New(root, cfg, ix)
	cT, sT := mcp.NewInMemoryTransports()

	serverCtx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Run(serverCtx, sT) }()

	cli := mcp.NewClient(&mcp.Implementation{Name: "gopher-mcp-test"}, nil)
	sess, err := cli.Connect(t.Context(), cT, nil)
	require.NoError(t, err)
	t.Cleanup(cancel)
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// textOf concatenates all *mcp.TextContent in a tool result.
func textOf(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}
