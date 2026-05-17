//go:build p4

package harness

import (
	"go/ast"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/analysis"

	"github.com/twinfer/gopher-mcp/internal/lintreg"
)

const testLintImport = "test/always-flag-foo"

// init registers a tiny test analyzer. It walks every FuncDecl and emits a
// diagnostic at any function literally named "Foo".
func init() {
	lintreg.Reset()
	lintreg.Register(testLintImport, func(cfg map[string]any) (*analysis.Analyzer, error) {
		return &analysis.Analyzer{
			Name: "always_flag_foo",
			Doc:  "flags every function literally named Foo",
			Run: func(pass *analysis.Pass) (any, error) {
				for _, f := range pass.Files {
					for _, decl := range f.Decls {
						fd, ok := decl.(*ast.FuncDecl)
						if !ok {
							continue
						}
						if fd.Name != nil && fd.Name.Name == "Foo" {
							pass.Report(analysis.Diagnostic{
								Pos:     fd.Pos(),
								Message: "function named Foo (test analyzer)",
							})
						}
					}
				}
				return nil, nil
			},
		}, nil
	})
}

// --- cite_resolve ---

func TestP4_CiteResolve_Hit(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "cite_resolve",
		Arguments: map[string]any{
			"citation": "vendor-src/sample.rs:4",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "unexpected error: %s", textOf(res))

	out := unmarshalP4[citeResolveOut](t, res)
	require.Equal(t, 4, out.Line)
	require.Contains(t, out.LineText, "meaningful line")
	require.Contains(t, out.File, "vendor-src/sample.rs")
}

func TestP4_CiteResolve_WithContext(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "cite_resolve",
		Arguments: map[string]any{
			"citation":      "vendor-src/sample.rs:4",
			"context_lines": 1,
		},
	})
	require.NoError(t, err)

	out := unmarshalP4[citeResolveOut](t, res)
	require.NotEmpty(t, out.Context)
	require.Equal(t, 3, out.ContextLine, "context should start one line before the target")
	require.True(t, strings.Count(out.Context, "\n") >= 1, "context should span at least 2 lines, got: %q", out.Context)
}

func TestP4_CiteResolve_NoMatch(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "cite_resolve",
		Arguments: map[string]any{
			"citation": "wholly/different/path.go:1",
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected IsError when no pattern matches; text was: %s", textOf(res))
}

// --- proto_field_xref ---

func TestP4_ProtoFieldXRef_SnakeName(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "proto_field_xref",
		Arguments: map[string]any{
			"message": "PartitionUpdate",
			"field":   "partition_id",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "unexpected error: %s", textOf(res))

	out := unmarshalP4[protoFieldXRefOut](t, res)
	require.Equal(t, "PartitionId", out.Field.GoField)
	require.Equal(t, "example.com/tiny/pkg/protogen.PartitionUpdate.PartitionId", out.Field.FieldQN)
	require.NotEmpty(t, out.Refs, "expected at least one ref site (reader.go reads PartitionId)")
}

func TestP4_ProtoFieldXRef_PascalName(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "proto_field_xref",
		Arguments: map[string]any{
			"message": "PartitionUpdate",
			"field":   "PartitionId",
		},
	})
	require.NoError(t, err)

	out := unmarshalP4[protoFieldXRefOut](t, res)
	require.Equal(t, "partition_id", out.Field.ProtoField)
	require.NotEmpty(t, out.Refs)
}

func TestP4_ProtoFieldXRef_Missing(t *testing.T) {
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "proto_field_xref",
		Arguments: map[string]any{
			"message": "PartitionUpdate",
			"field":   "does_not_exist",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "unknown field should be a clean 'no field' answer, not an error")
}

// --- lint ---

func TestP4_Lint_NoConfiguredAnalyzers(t *testing.T) {
	// The tiny fixture's .repo-mcp.yaml has lint: [], so the tool returns
	// a clean "no analyzers" message rather than an error.
	sess := startServer(t, "tiny")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "lint",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "expected non-error, got: %s", textOf(res))
	require.Contains(t, textOf(res), "no analyzers configured")
}

func TestP4_Lint_FlagsFoo(t *testing.T) {
	// The lintfix fixture references test/always-flag-foo and has one
	// FuncDecl named Foo (lib/lib.go). Expect exactly one diagnostic.
	sess := startServer(t, "lintfix")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "lint",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "unexpected error: %s", textOf(res))

	out := unmarshalP4[lintOut](t, res)
	require.Len(t, out.Diagnostics, 1, "expected exactly one Foo diagnostic, got: %+v", out.Diagnostics)
	require.Equal(t, "always_flag_foo", out.Diagnostics[0].Analyzer)
	require.Contains(t, out.Diagnostics[0].Message, "Foo")
}

func TestP4_Lint_RestrictByName(t *testing.T) {
	// Filtering by a non-matching analyzer name yields zero diagnostics.
	sess := startServer(t, "lintfix")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "lint",
		Arguments: map[string]any{
			"analyzers": []string{"nonexistent_analyzer"},
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	out := unmarshalP4[lintOut](t, res)
	require.Empty(t, out.Diagnostics)
}
