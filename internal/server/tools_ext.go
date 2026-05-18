package server

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/tools/go/analysis"

	"github.com/twinfer/gopher-mcp/internal/cite"
	"github.com/twinfer/gopher-mcp/internal/lintreg"
)

// --- cite_resolve ---

type citeResolveIn struct {
	Citation     string `json:"citation" jsonschema:"citation string to resolve, e.g. 'crates/foo/bar.rs:42'"`
	ContextLines int    `json:"context_lines,omitempty" jsonschema:"lines of context around the resolved line (default 0)"`
}

type citeResolveOut struct {
	Pattern     string `json:"pattern"`
	VendorRoot  string `json:"vendor_root"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	LineText    string `json:"line_text"`
	Context     string `json:"context,omitempty"`
	ContextLine int    `json:"context_line,omitempty"`
}

// --- proto_field_xref ---

type protoFieldXRefIn struct {
	Message   string `json:"message" jsonschema:"proto message name (Go type name)"`
	Field     string `json:"field" jsonschema:"proto field name (snake_case) or Go field name (PascalCase)"`
	ProtoGlob string `json:"proto_glob,omitempty" jsonschema:"restrict the proto-bearing packages to this glob"`
	RefGlob   string `json:"ref_glob,omitempty" jsonschema:"restrict ref search to packages matching this glob"`
	Limit     int    `json:"limit,omitempty" jsonschema:"max refs to return; 0 = no limit"`
}

type protoFieldXRefOut struct {
	Field     protoFieldHit      `json:"field"`
	Refs      []protoFieldRefHit `json:"refs"`
	Truncated bool               `json:"truncated,omitempty"`
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

// --- lint ---

type lintIn struct {
	Analyzers   []string `json:"analyzers,omitempty" jsonschema:"restrict to analyzer names; empty = all configured"`
	PackageGlob string   `json:"package_glob,omitempty" jsonschema:"restrict to packages matching this glob"`
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

// --- registration ---

func (s *Server) registerExtTools() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "cite_resolve",
		Description: "Resolve a citation string (e.g. 'crates/foo/bar.rs:42') against the " +
			"vendor roots declared in .repo-mcp.yaml `citations`. PREFER OVER manually " +
			"`grep`-walking vendored trees when a code comment cites foreign source. " +
			"Returns the file, line number, the line's text, and an optional context window.",
	}, s.handleCiteResolve)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "proto_field_xref",
		Description: "PREFER OVER `grep` for finding Go reads/writes of a protobuf field. " +
			"Field names like `Id` or `Name` collide constantly under text search; this " +
			"tool resolves the message + field through the generated struct, then returns " +
			"only true references to that field. Accepts snake_case (from .proto) or " +
			"PascalCase (Go) — either works.",
	}, s.handleProtoFieldXRef)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "lint",
		Description: "Run the analyzers configured in .repo-mcp.yaml `lint` over the index. " +
			"Each entry's `import` field maps to an analyzer that must be linked into this binary. " +
			"Use `analyzers` to restrict to specific names, or `package_glob` to restrict scope. " +
			"For repo-specific invariants (banned calls in deterministic scopes, etc.), " +
			"this catches violations a generic linter and `grep` will both miss.",
	}, s.handleLint)
}

// --- handlers ---

func (s *Server) handleCiteResolve(_ context.Context, _ *mcp.CallToolRequest, in citeResolveIn) (*mcp.CallToolResult, citeResolveOut, error) {
	if strings.TrimSpace(in.Citation) == "" {
		return nil, citeResolveOut{}, errors.New("citation is required")
	}
	if len(s.cfg.Citations) == 0 {
		return nil, citeResolveOut{}, errors.New("no citation patterns configured in .repo-mcp.yaml")
	}
	m, err := cite.Resolve(in.Citation, s.cfg.Citations, s.root, in.ContextLines)
	if err != nil {
		return nil, citeResolveOut{}, err
	}
	out := citeResolveOut{
		Pattern:     m.Pattern,
		VendorRoot:  m.VendorRoot,
		File:        m.File,
		Line:        m.Line,
		LineText:    m.LineText,
		Context:     m.Context,
		ContextLine: m.ContextLine,
	}
	return textResult(fmt.Sprintf("%s:%d: %s", m.File, m.Line, m.LineText)), out, nil
}

func (s *Server) handleProtoFieldXRef(_ context.Context, _ *mcp.CallToolRequest, in protoFieldXRefIn) (*mcp.CallToolResult, protoFieldXRefOut, error) {
	if strings.TrimSpace(in.Message) == "" {
		return nil, protoFieldXRefOut{}, errors.New("message is required")
	}
	if strings.TrimSpace(in.Field) == "" {
		return nil, protoFieldXRefOut{}, errors.New("field is required")
	}
	snap, err := s.snapshot()
	if err != nil {
		return nil, protoFieldXRefOut{}, err
	}
	protoGlob := in.ProtoGlob
	if protoGlob == "" && len(s.cfg.Proto) > 0 {
		// Default to the configured proto packages; join with '|' so glob
		// matchers see a single union pattern (our MatchPackagePath only
		// supports a single glob, so fall back to first entry if multiple).
		protoGlob = s.cfg.Proto[0].Import
	}
	field, refs, truncated := snap.ProtoFieldXRef(in.Message, in.Field, protoGlob, in.RefGlob, in.Limit)
	if field == nil {
		return textResult(fmt.Sprintf("no proto field %s.%s found", in.Message, in.Field)),
			protoFieldXRefOut{}, nil
	}
	out := protoFieldXRefOut{
		Field: protoFieldHit{
			ProtoMessage: field.ProtoMessage,
			ProtoField:   field.ProtoField,
			GoField:      field.GoField,
			PkgPath:      field.PkgPath,
			StructQN:     field.StructQN,
			FieldQN:      field.FieldQN,
		},
		Refs:      make([]protoFieldRefHit, 0, len(refs)),
		Truncated: truncated,
	}
	for _, r := range refs {
		out.Refs = append(out.Refs, protoFieldRefHit{File: r.File, Line: r.Line, Col: r.Col})
	}
	return textResult(fmt.Sprintf("%s.%s: %d ref(s)", field.StructQN, field.GoField, len(out.Refs))), out, nil
}

func (s *Server) handleLint(_ context.Context, _ *mcp.CallToolRequest, in lintIn) (*mcp.CallToolResult, lintOut, error) {
	snap, err := s.snapshot()
	if err != nil {
		return nil, lintOut{}, err
	}
	analyzers, err := s.buildLintAnalyzers(in.Analyzers)
	if err != nil {
		return nil, lintOut{}, err
	}
	if len(analyzers) == 0 {
		return textResult("no analyzers configured for this repo"), lintOut{}, nil
	}
	diags, err := snap.Lint(analyzers, in.PackageGlob)
	if err != nil {
		return nil, lintOut{}, err
	}
	out := lintOut{Diagnostics: make([]lintDiagHit, 0, len(diags))}
	for _, d := range diags {
		out.Diagnostics = append(out.Diagnostics, lintDiagHit{
			Analyzer: d.Analyzer,
			Severity: d.Severity,
			File:     d.File,
			Line:     d.Line,
			Col:      d.Col,
			Message:  d.Message,
			Category: d.Category,
		})
	}
	return textResult(fmt.Sprintf("%d diagnostic(s) from %d analyzer(s)", len(out.Diagnostics), len(analyzers))), out, nil
}

// buildLintAnalyzers materializes the configured analyzers (from
// .repo-mcp.yaml `lint`) via the registry, optionally filtered to those
// whose Name() appears in restrict.
func (s *Server) buildLintAnalyzers(restrict []string) ([]*analysis.Analyzer, error) {
	out := make([]*analysis.Analyzer, 0, len(s.cfg.Lint))
	for _, entry := range s.cfg.Lint {
		factory, err := lintreg.Get(entry.Import)
		if err != nil {
			return nil, err
		}
		a, err := factory(entry.Config)
		if err != nil {
			return nil, fmt.Errorf("analyzer %s: %w", entry.Import, err)
		}
		if a == nil {
			continue
		}
		if len(restrict) > 0 && !slices.Contains(restrict, a.Name) {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}
