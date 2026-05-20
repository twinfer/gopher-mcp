// Package astgrep is a tiny, kind-based AST query layer. Not a general DSL —
// just enough to answer "find every call to qualified-name X", "every type
// assertion to T", "every conversion to T", "every type that implements I".
package astgrep

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"regexp"

	"golang.org/x/tools/go/packages"

	"github.com/twinfer/gopher-mcp/internal/index"
	"github.com/twinfer/gopher-mcp/internal/util"
)

// Kind is the predicate kind. Tools accept these as strings.
type Kind string

const (
	KindCall       Kind = "call"
	KindTypeAssert Kind = "typeassert"
	KindConv       Kind = "conv"
	KindImplements Kind = "implements"
)

// Pattern is the input to Match.
type Pattern struct {
	Kind        Kind
	Func        string      // for KindCall: qualified callee, supports '*' wildcards
	NArgs       *int        // for KindCall: optional exact arg count
	Target      string      // for KindTypeAssert / KindConv: qualified target type
	Iface       string      // for KindImplements: qualified interface name
	PackageGlob string      // restrict to packages whose path matches
	Scope       index.Scope // tier scope; empty == workspace + direct
}

// Hit is one match.
type Hit struct {
	QName   string // for Implements: the type that satisfies; otherwise the matched callee/target
	PkgPath string
	File    string
	Line    int
	Col     int
}

// Match runs the pattern against the snapshot.
func Match(snap *index.Snapshot, pat Pattern) ([]Hit, error) {
	switch pat.Kind {
	case KindImplements:
		return matchImplements(snap, pat)
	case KindCall, KindTypeAssert, KindConv:
		return matchAST(snap, pat)
	default:
		return nil, errBadKind(pat.Kind)
	}
}

func matchImplements(snap *index.Snapshot, pat Pattern) ([]Hit, error) {
	if pat.Iface == "" {
		return nil, errMissing("iface")
	}
	ifaceSym, ok := snap.Syms.ByQN[index.StripInstantiation(pat.Iface)]
	if !ok {
		return nil, nil
	}
	iface, ok := ifaceSym.Obj.Type().Underlying().(*types.Interface)
	if !ok {
		return nil, errNotInterface(pat.Iface)
	}
	var hits []Hit
	for _, named := range snap.Syms.AllNamed {
		pkg := named.Obj().Pkg()
		if pkg == nil {
			continue
		}
		if !inScope(snap, pat.Scope, pkg.Path()) {
			continue
		}
		if !util.MatchPackagePath(pat.PackageGlob, pkg.Path()) {
			continue
		}
		// An interface T implements itself; skip that noise.
		if _, isIface := named.Underlying().(*types.Interface); isIface {
			continue
		}
		if types.Implements(named, iface) || types.Implements(types.NewPointer(named), iface) {
			pos := snap.Fset.Position(named.Obj().Pos())
			hits = append(hits, Hit{
				QName:   index.QualifyNamed(named),
				PkgPath: pkg.Path(),
				File:    pos.Filename,
				Line:    pos.Line,
				Col:     pos.Column,
			})
		}
	}
	return hits, nil
}

// inScope reports whether pkgPath's tier is included in scope. Cached lookup
// against snap.Tier — no allocations on the hot path beyond the set we'd
// build for each call. Kept simple; the caller usually wraps this in a tier
// set if iteration is hot.
func inScope(snap *index.Snapshot, scope index.Scope, pkgPath string) bool {
	tier := snap.TierOf(pkgPath)
	switch scope {
	case index.ScopeWorkspace:
		return tier == index.TierWorkspace
	case index.ScopeAll:
		return true
	default: // ScopeWorkspaceDirect, ""
		return tier == index.TierWorkspace || tier == index.TierDirect
	}
}

func matchAST(snap *index.Snapshot, pat Pattern) ([]Hit, error) {
	var funcRE *regexp.Regexp
	switch pat.Kind {
	case KindCall:
		if pat.Func == "" {
			return nil, errMissing("func")
		}
		funcRE = util.CompileNameGlob(pat.Func)
	case KindTypeAssert, KindConv:
		if pat.Target == "" {
			return nil, errMissing("target")
		}
	}
	seen := make(map[string]bool) // de-dup file:line:col

	var hits []Hit
	for _, pkg := range snap.PkgsForScope(pat.Scope) {
		if !util.MatchPackagePath(pat.PackageGlob, pkg.PkgPath) {
			continue
		}
		if pkg.TypesInfo == nil {
			continue
		}
		for _, f := range pkg.Syntax {
			ast.Inspect(f, func(n ast.Node) bool {
				switch pat.Kind {
				case KindCall:
					emitCall(snap, pkg, n, funcRE, pat.NArgs, &hits, seen)
				case KindTypeAssert:
					emitTypeAssert(snap, pkg, n, pat.Target, &hits, seen)
				case KindConv:
					emitConv(snap, pkg, n, pat.Target, &hits, seen)
				}
				return true
			})
		}
	}
	return hits, nil
}

func emitCall(snap *index.Snapshot, pkg *packages.Package, n ast.Node, re *regexp.Regexp, nargs *int, hits *[]Hit, seen map[string]bool) {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return
	}
	if nargs != nil && len(call.Args) != *nargs {
		return
	}
	callee := calleeQName(pkg, call.Fun)
	if callee == "" {
		return
	}
	if re != nil && !re.MatchString(callee) {
		return
	}
	pos := snap.Fset.Position(call.Pos())
	key := posKey(pos)
	if seen[key] {
		return
	}
	seen[key] = true
	*hits = append(*hits, Hit{
		QName:   callee,
		PkgPath: pkg.PkgPath,
		File:    pos.Filename,
		Line:    pos.Line,
		Col:     pos.Column,
	})
}

// calleeQName resolves a call expression's callee to its qualified name. For
// methods on a value, this returns the method's qualified name. Returns ""
// when the callee is a builtin, a func literal, or otherwise unresolvable.
func calleeQName(pkg *packages.Package, fun ast.Expr) string {
	switch e := fun.(type) {
	case *ast.Ident:
		obj := pkg.TypesInfo.ObjectOf(e)
		if obj == nil {
			return ""
		}
		return index.QualifyObject(obj)
	case *ast.SelectorExpr:
		// pkg-qualified call (fmt.Println), or a method call (recv.Method).
		obj := pkg.TypesInfo.ObjectOf(e.Sel)
		if obj == nil {
			return ""
		}
		return index.QualifyObject(obj)
	}
	return ""
}

func emitTypeAssert(snap *index.Snapshot, pkg *packages.Package, n ast.Node, target string, hits *[]Hit, seen map[string]bool) {
	ta, ok := n.(*ast.TypeAssertExpr)
	if !ok || ta.Type == nil { // skip x.(type) inside type switches
		return
	}
	t := pkg.TypesInfo.TypeOf(ta.Type)
	if t == nil {
		return
	}
	if !typeMatches(t, target) {
		return
	}
	emitHit(snap, pkg, ta.Pos(), target, hits, seen)
}

func emitConv(snap *index.Snapshot, pkg *packages.Package, n ast.Node, target string, hits *[]Hit, seen map[string]bool) {
	// Conversions are call-shaped: T(x). The callee is a type, not a func.
	call, ok := n.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return
	}
	tv, ok := pkg.TypesInfo.Types[call.Fun]
	if !ok || !tv.IsType() {
		return
	}
	if !typeMatches(tv.Type, target) {
		return
	}
	emitHit(snap, pkg, call.Pos(), target, hits, seen)
}

func emitHit(snap *index.Snapshot, pkg *packages.Package, p token.Pos, qname string, hits *[]Hit, seen map[string]bool) {
	pos := snap.Fset.Position(p)
	key := posKey(pos)
	if seen[key] {
		return
	}
	seen[key] = true
	*hits = append(*hits, Hit{
		QName:   index.StripInstantiation(qname),
		PkgPath: pkg.PkgPath,
		File:    pos.Filename,
		Line:    pos.Line,
		Col:     pos.Column,
	})
}

func posKey(p token.Position) string {
	return fmt.Sprintf("%s:%d:%d", p.Filename, p.Line, p.Column)
}

func errBadKind(k Kind) error       { return fmt.Errorf("astgrep: unknown kind %q", k) }
func errMissing(field string) error { return fmt.Errorf("astgrep: missing required field %q", field) }
func errNotInterface(qn string) error {
	return fmt.Errorf("astgrep: %q is not an interface type", qn)
}

// typeMatches reports whether t's qualified name equals target (modulo
// instantiation suffixes and pointer wrappers).
func typeMatches(t types.Type, target string) bool {
	target = index.StripInstantiation(target)
	// Allow target = "*pkg.T" by checking pointer types too.
	wantPtr := len(target) > 0 && target[0] == '*'
	if wantPtr {
		ptr, ok := t.(*types.Pointer)
		if !ok {
			return false
		}
		t = ptr.Elem()
		target = target[1:]
	}
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	return index.QualifyNamed(named) == target
}
