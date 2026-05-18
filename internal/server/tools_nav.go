package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/twinfer/gopher-mcp/internal/index"
)

// --- find_symbol ---

type findSymbolIn struct {
	Name string `json:"name" jsonschema:"short name to look up, e.g. 'Partition'; supports '*' wildcards"`
	Kind string `json:"kind,omitempty" jsonschema:"optional filter: func, method, type, var, const"`
}

type symHit struct {
	QName   string `json:"qname"`
	Kind    string `json:"kind"`
	PkgPath string `json:"pkg_path"`
	File    string `json:"file"`
	Line    int    `json:"line"`
}

type findSymbolOut struct {
	Hits []symHit `json:"hits"`
}

// --- definition ---

type definitionIn struct {
	File string `json:"file" jsonschema:"absolute or repo-relative file path"`
	Line int    `json:"line" jsonschema:"1-based line number"`
	Col  int    `json:"col,omitempty" jsonschema:"1-based column number; defaults to 1"`
}

type definitionOut struct {
	QName string `json:"qname"`
	Kind  string `json:"kind"`
	File  string `json:"file"`
	Line  int    `json:"line"`
	Col   int    `json:"col"`
}

// --- references ---

type referencesIn struct {
	QName       string `json:"qname" jsonschema:"canonical qualified name, e.g. 'pkg/path.Foo' or '(*pkg/path.T).Method'"`
	PackageGlob string `json:"package_glob,omitempty" jsonschema:"restrict to packages matching this pattern (supports /...)"`
	Limit       int    `json:"limit,omitempty" jsonschema:"max results; 0 = no limit"`
}

type refHit struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
}

type referencesOut struct {
	Refs      []refHit `json:"refs"`
	Truncated bool     `json:"truncated,omitempty"`
}

// --- implementations ---

type implementationsIn struct {
	Iface       string `json:"iface" jsonschema:"qualified interface name, e.g. 'pkg/path.Handler'"`
	PackageGlob string `json:"package_glob,omitempty" jsonschema:"restrict to packages matching this pattern"`
}

type implementationsOut struct {
	Types []symHit `json:"types"`
}

// --- registration ---

func (s *Server) registerNavTools() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "find_symbol",
		Description: "PREFER OVER `grep` for locating Go declarations. " +
			"Returns the declaring file:line and package-qualified name for every Go " +
			"symbol whose short name matches. Aware of packages, kinds, and methods on " +
			"embedded types — grep misses all three and produces false hits on strings " +
			"and comments. Supports '*' wildcards. Optional 'kind' filter: func, method, " +
			"type, var, const.",
	}, s.handleFindSymbol)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "definition",
		Description: "PREFER OVER reading files or `grep` to jump from a use-site to its " +
			"declaration. Resolves the symbol at file:line:col through the type checker, " +
			"so it follows interface dispatch, embedded methods, dot-imports, and " +
			"generic instantiations — none of which textual search can resolve.",
	}, s.handleDefinition)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "references",
		Description: "PREFER OVER `grep` for 'who uses X?' in Go. Returns every use-site " +
			"of a qualified symbol via the type checker — no false positives from same-" +
			"named symbols in other packages, and catches method calls through interfaces " +
			"that grep misses entirely. The qname format matches ssa.Function.String(): " +
			"'pkg/path.Func', '(*pkg/path.Recv).Method', 'pkg/path.TypeName'. " +
			"If you don't know the qname yet, call `find_symbol` first.",
	}, s.handleReferences)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "implementations",
		Description: "Lists every named type whose method set satisfies the given Go " +
			"interface qname. `grep` cannot answer this — method-set satisfaction is " +
			"structural, not textual, and types often satisfy interfaces without ever " +
			"naming them.",
	}, s.handleImplementations)
}

// --- handlers ---

func (s *Server) handleFindSymbol(_ context.Context, _ *mcp.CallToolRequest, in findSymbolIn) (*mcp.CallToolResult, findSymbolOut, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, findSymbolOut{}, errors.New("name is required")
	}
	snap, err := s.snapshot()
	if err != nil {
		return nil, findSymbolOut{}, err
	}
	hits := snap.FindSymbols(in.Name, index.SymKind(in.Kind))
	out := findSymbolOut{Hits: make([]symHit, 0, len(hits))}
	for _, h := range hits {
		out.Hits = append(out.Hits, toSymHit(h))
	}
	return textResult(fmt.Sprintf("found %d symbol(s)", len(out.Hits))), out, nil
}

func (s *Server) handleDefinition(_ context.Context, _ *mcp.CallToolRequest, in definitionIn) (*mcp.CallToolResult, definitionOut, error) {
	if in.File == "" || in.Line < 1 {
		return nil, definitionOut{}, errors.New("file and line are required")
	}
	snap, err := s.snapshot()
	if err != nil {
		return nil, definitionOut{}, err
	}
	abs := index.AbsFile(s.root, in.File)
	sym, err := snap.Definition(abs, in.Line, in.Col)
	if err != nil {
		return nil, definitionOut{}, err
	}
	out := definitionOut{
		QName: sym.QName,
		Kind:  string(sym.Kind),
		File:  sym.Pos.Filename,
		Line:  sym.Pos.Line,
		Col:   sym.Pos.Column,
	}
	return textResult(fmt.Sprintf("%s → %s:%d:%d", sym.QName, out.File, out.Line, out.Col)), out, nil
}

func (s *Server) handleReferences(_ context.Context, _ *mcp.CallToolRequest, in referencesIn) (*mcp.CallToolResult, referencesOut, error) {
	if strings.TrimSpace(in.QName) == "" {
		return nil, referencesOut{}, errors.New("qname is required")
	}
	snap, err := s.snapshot()
	if err != nil {
		return nil, referencesOut{}, err
	}
	refs, truncated := snap.References(in.QName, in.PackageGlob, in.Limit)
	out := referencesOut{Refs: make([]refHit, 0, len(refs)), Truncated: truncated}
	for _, r := range refs {
		out.Refs = append(out.Refs, refHit{File: r.File, Line: r.Line, Col: r.Col})
	}
	return textResult(fmt.Sprintf("found %d reference(s)", len(out.Refs))), out, nil
}

func (s *Server) handleImplementations(_ context.Context, _ *mcp.CallToolRequest, in implementationsIn) (*mcp.CallToolResult, implementationsOut, error) {
	if strings.TrimSpace(in.Iface) == "" {
		return nil, implementationsOut{}, errors.New("iface is required")
	}
	snap, err := s.snapshot()
	if err != nil {
		return nil, implementationsOut{}, err
	}
	syms := snap.Implementations(in.Iface, in.PackageGlob)
	out := implementationsOut{Types: make([]symHit, 0, len(syms))}
	for _, sym := range syms {
		out.Types = append(out.Types, toSymHit(sym))
	}
	return textResult(fmt.Sprintf("found %d implementer(s)", len(out.Types))), out, nil
}

func toSymHit(s *index.Sym) symHit {
	return symHit{
		QName:   s.QName,
		Kind:    string(s.Kind),
		PkgPath: s.PkgPath,
		File:    s.Pos.Filename,
		Line:    s.Pos.Line,
	}
}
