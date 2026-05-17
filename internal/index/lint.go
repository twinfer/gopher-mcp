package index

import (
	"fmt"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"

	"github.com/twinfer/gopher-mcp/internal/util"
)

// LintDiag is one diagnostic emitted by an analyzer.
type LintDiag struct {
	Analyzer string
	Severity string // "error" by default; analyzers may not set this
	File     string
	Line     int
	Col      int
	Message  string
	Category string
}

// Lint runs the supplied analyzers over packages matching pkgGlob. The
// analyzers must already be configured (factories applied). An empty
// analyzers slice is an error — `lint` is only useful with at least one.
func (s *Snapshot) Lint(analyzers []*analysis.Analyzer, pkgGlob string) ([]LintDiag, error) {
	if len(analyzers) == 0 {
		return nil, fmt.Errorf("no analyzers to run")
	}
	pkgs := s.Pkgs
	if pkgGlob != "" {
		filtered := pkgs[:0:0]
		for _, p := range pkgs {
			if util.MatchPackagePath(pkgGlob, p.PkgPath) {
				filtered = append(filtered, p)
			}
		}
		pkgs = filtered
	}
	if len(pkgs) == 0 {
		return nil, nil
	}
	graph, err := checker.Analyze(analyzers, pkgs, nil)
	if err != nil {
		return nil, fmt.Errorf("checker.Analyze: %w", err)
	}
	var out []LintDiag
	for act := range graph.All() {
		for _, d := range act.Diagnostics {
			pos := s.Fset.Position(d.Pos)
			out = append(out, LintDiag{
				Analyzer: act.Analyzer.Name,
				Severity: "error",
				File:     pos.Filename,
				Line:     pos.Line,
				Col:      pos.Column,
				Message:  d.Message,
				Category: d.Category,
			})
		}
	}
	return out, nil
}
