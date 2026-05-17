package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/twinfer/gopher-mcp/internal/astgrep"
)

type astGrepIn struct {
	Kind        string `json:"kind" jsonschema:"one of: call, typeassert, conv, implements"`
	Func        string `json:"func,omitempty" jsonschema:"for kind=call: qualified callee name; supports '*' wildcards"`
	NArgs       *int   `json:"n_args,omitempty" jsonschema:"for kind=call: optional exact arg count"`
	Target      string `json:"target,omitempty" jsonschema:"for kind=typeassert or conv: qualified target type; '*pkg.T' for pointer"`
	Iface       string `json:"iface,omitempty" jsonschema:"for kind=implements: qualified interface name"`
	PackageGlob string `json:"package_glob,omitempty" jsonschema:"restrict to packages matching this pattern"`
}

type astHit struct {
	QName   string `json:"qname"`
	PkgPath string `json:"pkg_path"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
}

type astGrepOut struct {
	Hits []astHit `json:"hits"`
}

func (s *Server) registerASTTools() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "ast_grep",
		Description: "Structural AST search. Pick a kind:\n" +
			"  - call: find calls to a qualified function (use 'func', optional 'n_args')\n" +
			"  - typeassert: find type assertions to a qualified type (use 'target')\n" +
			"  - conv: find conversions to a qualified type (use 'target')\n" +
			"  - implements: find types whose method set satisfies an interface (use 'iface')\n" +
			"All kinds accept 'package_glob' to restrict scope (e.g. 'github.com/foo/...').",
	}, s.handleASTGrep)
}

func (s *Server) handleASTGrep(_ context.Context, _ *mcp.CallToolRequest, in astGrepIn) (*mcp.CallToolResult, astGrepOut, error) {
	if strings.TrimSpace(in.Kind) == "" {
		return nil, astGrepOut{}, errors.New("kind is required")
	}
	snap, err := s.snapshot()
	if err != nil {
		return nil, astGrepOut{}, err
	}
	hits, err := astgrep.Match(snap, astgrep.Pattern{
		Kind:        astgrep.Kind(in.Kind),
		Func:        in.Func,
		NArgs:       in.NArgs,
		Target:      in.Target,
		Iface:       in.Iface,
		PackageGlob: in.PackageGlob,
	})
	if err != nil {
		return nil, astGrepOut{}, err
	}
	out := astGrepOut{Hits: make([]astHit, 0, len(hits))}
	for _, h := range hits {
		out.Hits = append(out.Hits, astHit{
			QName:   h.QName,
			PkgPath: h.PkgPath,
			File:    h.File,
			Line:    h.Line,
			Col:     h.Col,
		})
	}
	return textResult(fmt.Sprintf("found %d hit(s)", len(out.Hits))), out, nil
}
