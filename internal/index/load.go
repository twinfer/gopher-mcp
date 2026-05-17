package index

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/tools/go/packages"
)

const loadMode = packages.NeedName |
	packages.NeedFiles |
	packages.NeedCompiledGoFiles |
	packages.NeedSyntax |
	packages.NeedTypes |
	packages.NeedTypesInfo |
	packages.NeedTypesSizes |
	packages.NeedImports |
	packages.NeedDeps |
	packages.NeedModule

// loadPackages loads ./... from root. Returns the packages, a flat list of
// per-package errors (empty if everything typechecked), and a hard error
// only when nothing loaded at all.
func loadPackages(ctx context.Context, root string, buildTags []string) ([]*packages.Package, []packages.Error, error) {
	cfg := &packages.Config{
		Mode:    loadMode,
		Dir:     root,
		Tests:   true,
		Context: ctx,
	}
	if len(buildTags) > 0 {
		cfg.BuildFlags = []string{"-tags=" + joinTags(buildTags)}
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, nil, fmt.Errorf("packages.Load: %w", err)
	}
	if len(pkgs) == 0 {
		return nil, nil, fmt.Errorf("packages.Load: no packages matched ./... in %s", root)
	}
	var loadErrs []packages.Error
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		loadErrs = append(loadErrs, p.Errors...)
	})
	return pkgs, loadErrs, nil
}

func joinTags(tags []string) string {
	var out strings.Builder
	for i, t := range tags {
		if i > 0 {
			out.WriteString(",")
		}
		out.WriteString(t)
	}
	return out.String()
}
