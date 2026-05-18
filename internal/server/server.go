package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/mod/modfile"

	"github.com/twinfer/gopher-mcp/internal/config"
	"github.com/twinfer/gopher-mcp/internal/index"
)

const (
	Name    = "gopher-mcp"
	Version = "0.1.0"
)

// Server bundles the MCP server with the state its tool handlers need.
type Server struct {
	root string
	cfg  config.RepoConfig
	ix   *index.Index
	mcp  *mcp.Server
}

// New wires resources + tools onto a fresh MCP server. Call Run to start it.
// ix may be nil for P1-only servers; index-backed tools error out cleanly when
// the snapshot is missing.
func New(root string, cfg config.RepoConfig, ix *index.Index) *Server {
	s := &Server{
		root: root,
		cfg:  cfg,
		mcp: mcp.NewServer(
			&mcp.Implementation{Name: Name, Version: Version},
			&mcp.ServerOptions{Instructions: buildInstructions(root, ix != nil)},
		),
		ix: ix,
	}
	s.registerResources()
	s.registerExecTools()
	if ix != nil {
		s.registerNavTools()
		s.registerASTTools()
		s.registerCallgraphTools()
		s.registerExtTools()
	}
	return s
}

// buildInstructions returns the server-level MCP instructions string. Clients
// surface this once at session start, so we use it to anchor repo-specific
// framing the model would otherwise lack: which Go module this server
// indexes, and a routing table that nudges the model away from `grep` for
// type-aware queries it can't answer correctly.
//
// If we can't read the module path, we degrade gracefully — generic framing
// is still better than no framing.
func buildInstructions(root string, hasIndex bool) string {
	module := readModulePath(root)
	var where string
	switch {
	case module != "" && hasIndex:
		where = fmt.Sprintf("This server indexes the Go module `%s` rooted at `%s`.", module, root)
	case hasIndex:
		where = fmt.Sprintf("This server indexes the Go module rooted at `%s`.", root)
	default:
		where = fmt.Sprintf("This server exposes `go doc` / `go list` / `go test` for the module at `%s`. The type-aware navigation tools are disabled (index not loaded).", root)
	}
	if !hasIndex {
		return where
	}
	return where + `

When working on Go code in this module, prefer these MCP tools over ` + "`grep`/`rg`" + ` or reading files to hunt for things — they consult the type checker and callgraph, so they catch interface dispatch, embedded methods, dot-imports, and generic instantiations that textual search silently misses:

  - Locate a Go declaration            → find_symbol         (not ` + "`grep \"func Foo\"`" + `)
  - Jump from a use-site to its decl   → definition          (not reading the file)
  - Find every use-site of a symbol    → references          (not ` + "`grep -r \"Foo(\"`" + `)
  - List types implementing an iface   → implementations     (grep can't — it's a structural match)
  - Match Go syntax (calls, asserts)   → ast_grep
  - Caller/callee edges, reachability  → callers / callees / reverse_trace
  - Readers/writers of a proto field   → proto_field_xref    (not ` + "`grep \"FieldName\"`" + `)
  - Resolve a vendored-source citation → cite_resolve

Symbol qnames match ` + "`ssa.Function.String()`" + `: ` + "`pkg/path.Func`" + `, ` + "`(*pkg/path.Recv).Method`" + `, ` + "`pkg/path.TypeName`" + `. If you don't know the qname, call ` + "`find_symbol`" + ` first.

Grep is still the right tool for: comments, log/error strings, config files, non-Go files, and anything outside this module.`
}

// readModulePath returns the module path declared in <root>/go.mod, or ""
// if the file is missing or malformed. Errors are swallowed by design — the
// caller falls back to generic framing.
func readModulePath(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	return modfile.ModulePath(data)
}

// Run serves until ctx is canceled or the transport closes.
func (s *Server) Run(ctx context.Context, t mcp.Transport) error {
	return s.mcp.Run(ctx, t)
}

// snapshot returns the current index snapshot or an error if none has been loaded.
func (s *Server) snapshot() (*index.Snapshot, error) {
	if s.ix == nil {
		return nil, errors.New("index not configured for this server")
	}
	snap := s.ix.Snapshot()
	if snap == nil {
		return nil, errors.New("index not yet loaded")
	}
	return snap, nil
}
