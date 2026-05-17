// Package bannedinscope flags calls to "banned" qualified names within a set
// of "scoped" packages. It's the determinism guard: e.g. handler code under
// pkg/sdk/... must not call time.Now or net/http.Get directly.
//
// Config (under .repo-mcp.yaml `lint`):
//
//   - import: github.com/twinfer/gopher-mcp/pkg/analyzers/bannedinscope
//     config:
//     banned: [time.Now, "math/rand.*", net/http.Get, os.Getenv]
//     scope_packages: [example.com/repo/pkg/sdk/...]
//
// `banned` entries match the callee's qualified name (e.g. `pkg/path.Func`,
// `(*pkg/path.T).Method`); '*' is a wildcard. `scope_packages` restricts
// where the analyzer runs; an empty list means all packages.
package bannedinscope

import (
	"fmt"
	"go/ast"
	"go/types"
	"regexp"

	"golang.org/x/tools/go/analysis"

	"github.com/twinfer/gopher-mcp/internal/index"
	"github.com/twinfer/gopher-mcp/internal/lintreg"
	"github.com/twinfer/gopher-mcp/internal/util"
)

// RegistryKey is the YAML `import:` value that selects this analyzer.
const RegistryKey = "github.com/twinfer/gopher-mcp/pkg/analyzers/bannedinscope"

func init() {
	lintreg.Register(RegistryKey, Factory)
}

// Factory builds the analyzer from a YAML config map.
func Factory(cfg map[string]any) (*analysis.Analyzer, error) {
	banned, err := stringSlice(cfg, "banned")
	if err != nil {
		return nil, err
	}
	if len(banned) == 0 {
		return nil, fmt.Errorf("bannedinscope: 'banned' must be a non-empty list of qualified names")
	}
	scopes, err := stringSlice(cfg, "scope_packages")
	if err != nil {
		return nil, err
	}
	res := make([]*regexp.Regexp, 0, len(banned))
	for _, b := range banned {
		re := util.CompileNameGlob(b)
		if re == nil {
			continue
		}
		res = append(res, re)
	}
	if len(res) == 0 {
		return nil, fmt.Errorf("bannedinscope: no valid banned patterns")
	}
	return &analysis.Analyzer{
		Name: "bannedinscope",
		Doc:  "flags calls to banned qualified names within scoped packages",
		Run:  runner(res, scopes),
	}, nil
}

func runner(banned []*regexp.Regexp, scopes []string) func(*analysis.Pass) (any, error) {
	return func(pass *analysis.Pass) (any, error) {
		if pass.Pkg == nil {
			return nil, nil
		}
		if !inScope(pass.Pkg.Path(), scopes) {
			return nil, nil
		}
		for _, f := range pass.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				qn := calleeQName(pass.TypesInfo, call.Fun)
				if qn == "" {
					return true
				}
				for _, re := range banned {
					if re.MatchString(qn) {
						pass.Report(analysis.Diagnostic{
							Pos:      call.Pos(),
							End:      call.End(),
							Category: "bannedinscope",
							Message:  fmt.Sprintf("call to banned %s in scope %s", qn, pass.Pkg.Path()),
						})
						return true
					}
				}
				return true
			})
		}
		return nil, nil
	}
}

func inScope(pkgPath string, scopes []string) bool {
	if len(scopes) == 0 {
		return true
	}
	for _, s := range scopes {
		if util.MatchPackagePath(s, pkgPath) {
			return true
		}
	}
	return false
}

func calleeQName(info *types.Info, fun ast.Expr) string {
	if info == nil {
		return ""
	}
	switch e := fun.(type) {
	case *ast.Ident:
		if obj := info.ObjectOf(e); obj != nil {
			return index.QualifyObject(obj)
		}
	case *ast.SelectorExpr:
		if obj := info.ObjectOf(e.Sel); obj != nil {
			return index.QualifyObject(obj)
		}
	}
	return ""
}

func stringSlice(cfg map[string]any, key string) ([]string, error) {
	v, ok := cfg[key]
	if !ok || v == nil {
		return nil, nil
	}
	raw, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("bannedinscope: %s must be a list of strings", key)
	}
	out := make([]string, 0, len(raw))
	for i, e := range raw {
		s, ok := e.(string)
		if !ok {
			return nil, fmt.Errorf("bannedinscope: %s[%d] must be a string, got %T", key, i, e)
		}
		out = append(out, s)
	}
	return out, nil
}
