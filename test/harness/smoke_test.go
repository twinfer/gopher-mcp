//go:build smoke

package harness

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// TestSmoke_Reflow exercises the installed gopher-mcp binary as a real
// subprocess against the reflow repo. Requires:
//   - $HOME/go/bin/gopher-mcp installed (go install ./cmd/gopher-mcp)
//   - /Users/khalid/dev/x/reflow checkout with .repo-mcp.yaml present
//
// Run with: go test -tags=smoke -v -count=1 ./test/harness/...
func TestSmoke_Reflow(t *testing.T) {
	const (
		binary    = "/Users/khalid/go/bin/gopher-mcp"
		reflowDir = "/Users/khalid/dev/x/reflow"
	)
	if _, err := os.Stat(binary); err != nil {
		t.Skipf("binary not installed at %s: %v", binary, err)
	}
	if _, err := os.Stat(reflowDir + "/go.mod"); err != nil {
		t.Skipf("reflow checkout not at %s: %v", reflowDir, err)
	}

	cmd := exec.Command(binary, "-root", reflowDir)
	cmd.Stderr = os.Stderr

	cli := mcp.NewClient(&mcp.Implementation{Name: "gopher-mcp-smoke"}, nil)
	sess, err := cli.Connect(t.Context(), &mcp.CommandTransport{Command: cmd}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })

	t.Run("ListResources", func(t *testing.T) {
		res, err := sess.ListResources(t.Context(), &mcp.ListResourcesParams{})
		require.NoError(t, err)
		require.Len(t, res.Resources, 3)
		uris := make([]string, 0, 3)
		for _, r := range res.Resources {
			uris = append(uris, r.URI)
		}
		require.ElementsMatch(t, []string{
			"repo:CLAUDE.md",
			"repo:internal/engine/CLAUDE.md",
			"repo:durable-execution-go-sad.md",
		}, uris)
	})

	t.Run("ReadResource_RootClaude", func(t *testing.T) {
		res, err := sess.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "repo:CLAUDE.md"})
		require.NoError(t, err)
		require.Len(t, res.Contents, 1)
		require.Contains(t, res.Contents[0].Text, "Reflow is a")
	})

	t.Run("ReadResource_EngineClaude", func(t *testing.T) {
		res, err := sess.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "repo:internal/engine/CLAUDE.md"})
		require.NoError(t, err)
		require.Len(t, res.Contents, 1)
		require.Contains(t, res.Contents[0].Text, "Goroutine model")
	})

	t.Run("ReadResource_Rejects_Undeclared", func(t *testing.T) {
		_, err := sess.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "repo:go.mod"})
		require.Error(t, err)
	})

	t.Run("ListTools", func(t *testing.T) {
		res, err := sess.ListTools(t.Context(), &mcp.ListToolsParams{})
		require.NoError(t, err)
		names := make([]string, 0, len(res.Tools))
		for _, tl := range res.Tools {
			names = append(names, tl.Name)
		}
		require.Contains(t, names, "go_doc")
		require.Contains(t, names, "go_list_modules")
	})

	t.Run("GoDoc_ReflowPkg", func(t *testing.T) {
		// `go doc` on a reflow-internal package — must resolve relative to reflow's module root.
		res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "go_doc",
			Arguments: map[string]any{"path": "./pkg/sdk"},
		})
		require.NoError(t, err)
		text := textOf(res)
		require.NotEmpty(t, text)
		require.True(t,
			strings.Contains(text, "Handler") || strings.Contains(text, "package sdk"),
			"expected reflow pkg/sdk content, got: %s", truncate(text, 200))
	})

	t.Run("GoListModules_FindsReflow", func(t *testing.T) {
		res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
			Name: "go_list_modules",
		})
		require.NoError(t, err)
		text := textOf(res)
		require.Contains(t, text, "reflow")
	})

	// --- P2 nav tools against reflow's real source ---

	t.Run("FindSymbol_Partition", func(t *testing.T) {
		res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "find_symbol",
			Arguments: map[string]any{"name": "Partition", "kind": "type"},
		})
		require.NoError(t, err)
		text := textOf(res)
		require.True(t,
			strings.Contains(text, "1 symbol") || strings.Contains(text, "symbol(s)"),
			"unexpected text response: %s", text)
	})

	t.Run("Implementations_Store", func(t *testing.T) {
		// storage.Store has a Pebble impl and an in-memory impl in tree.
		res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
			Name: "implementations",
			Arguments: map[string]any{
				"iface": "github.com/twinfer/reflow/internal/storage.Store",
			},
		})
		require.NoError(t, err)
		require.NotContains(t, textOf(res), "0 implementer")
	})

	t.Run("Callees_PartitionUpdate", func(t *testing.T) {
		// (*Partition).Update is the raft state-machine entry point.
		// CHA will produce at least one callee; we just verify the tool runs
		// and returns a non-empty edge set.
		res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
			Name: "callees",
			Arguments: map[string]any{
				"qname": "(*github.com/twinfer/reflow/internal/engine.Partition).Update",
			},
		})
		require.NoError(t, err)
		text := textOf(res)
		require.Contains(t, text, "callee(s)")
		require.NotContains(t, text, "0 callee", "expected non-empty callees for Partition.Update, got: %s", text)
	})

	t.Run("Lint_BannedInScope", func(t *testing.T) {
		// reflow's .repo-mcp.yaml enables bannedinscope against
		// pkg/sdk/... and examples/... — just verify the tool runs end-to-end.
		// Zero diagnostics is the happy case (handler code stays deterministic).
		res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
			Name: "lint",
		})
		require.NoError(t, err)
		require.False(t, res.IsError, "lint should not error: %s", textOf(res))
		require.Contains(t, textOf(res), "diagnostic(s)")
		t.Logf("reflow bannedinscope result: %s", textOf(res))
	})

	t.Run("CiteResolve_RestateMirror", func(t *testing.T) {
		// reflow's timer_service.go cites this exact line range.
		res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
			Name: "cite_resolve",
			Arguments: map[string]any{
				"citation": "crates/timer/src/lib.rs:21",
			},
		})
		require.NoError(t, err)
		require.False(t, res.IsError, "cite_resolve errored: %s", textOf(res))
		text := textOf(res)
		require.Contains(t, text, "lib.rs")
		require.Contains(t, text, ":21")
	})

	t.Run("ProtoFieldXref_EnginevInvocationId", func(t *testing.T) {
		// enginev1.InvocationId.partition_key is read all over the engine.
		res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
			Name: "proto_field_xref",
			Arguments: map[string]any{
				"message": "InvocationId",
				"field":   "partition_key",
			},
		})
		require.NoError(t, err)
		require.False(t, res.IsError, "proto_field_xref errored: %s", textOf(res))
		text := textOf(res)
		require.Contains(t, text, "ref(s)")
		require.NotContains(t, text, "0 ref(s)",
			"expected at least one Go read site for partition_key, got: %s", text)
		t.Logf("proto_field_xref result: %s", text)
	})

	t.Run("ReverseTrace_ApplyPath", func(t *testing.T) {
		// From (*Partition).Update, find a path to a leaf in the engine.
		// Just verify the tool runs and the lookup of the configured entry
		// point resolves — a real path may or may not exist depending on
		// what the engine actually calls from Update.
		res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
			Name: "reverse_trace",
			Arguments: map[string]any{
				"target": "(*github.com/twinfer/reflow/internal/engine.PartitionRunner).dispatchActions",
				"entry_points": []string{
					"(*github.com/twinfer/reflow/internal/engine.Partition).Update",
				},
			},
		})
		require.NoError(t, err)
		require.False(t, res.IsError, "reverse_trace errored: %s", textOf(res))
		t.Logf("reverse_trace result: %s", textOf(res))
	})

	t.Run("RunTest_ReflowPkgSdk", func(t *testing.T) {
		// Smoke: invoke the run_test tool against a small, fast reflow
		// package. We don't care about pass/fail — only that the tool
		// drives `go test` and returns output.
		res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
			Name: "run_test",
			Arguments: map[string]any{
				"packages": "./pkg/sdk/...",
				"run":      "^TestNothingMatchesThisFilter$",
			},
		})
		require.NoError(t, err)
		text := textOf(res)
		// With a no-match -run filter, every package reports "no tests to run".
		require.Contains(t, text, "no tests to run")
	})

	t.Run("AstGrep_TimeNow_InSDKExamples", func(t *testing.T) {
		// Determinism rule: handler code (under pkg/sdk and examples) must
		// not call time.Now directly. Verify the tool runs and reports a
		// count. Zero hits is the happy case; non-zero is an actual finding.
		res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
			Name: "ast_grep",
			Arguments: map[string]any{
				"kind":         "call",
				"func":         "time.Now",
				"package_glob": "github.com/twinfer/reflow/examples/...",
			},
		})
		require.NoError(t, err)
		require.Contains(t, textOf(res), "hit(s)")
	})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
