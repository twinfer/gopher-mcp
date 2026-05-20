package index

import (
	"fmt"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/packages"
)

// SymKind enumerates the symbol kinds find_symbol accepts.
type SymKind string

const (
	KindFunc   SymKind = "func"
	KindMethod SymKind = "method"
	KindType   SymKind = "type"
	KindVar    SymKind = "var"
	KindConst  SymKind = "const"
)

// Sym is one entry in the symbol table.
type Sym struct {
	Kind    SymKind
	QName   string       // canonical, matches ssa.Function.String()
	SName   string       // short name (no package, no receiver)
	Obj     types.Object // pointer-stable within a Snapshot
	PkgPath string
	Pos     token.Position
	Tier    PkgTier // workspace / direct / indirect / stdlib
}

// SymbolTable is built once per Snapshot from packages.Package metadata.
type SymbolTable struct {
	BySN     map[string][]*Sym // short name → all matching symbols
	ByQN     map[string]*Sym   // qualified name → single symbol
	AllNamed []*types.Named    // every named type, for implementations queries
}

// QualifyObject returns the canonical qualified name for obj. Matches
// ssa.Function.String() for funcs and methods; uses pkg.Path + "." + name
// for types/vars/consts.
//
// Forms:
//
//	pkg/path.FuncName
//	(*pkg/path.Recv).Method
//	(pkg/path.Recv).Method
//	pkg/path.TypeName
func QualifyObject(obj types.Object) string {
	if obj == nil || obj.Pkg() == nil {
		// Builtins and the universe scope have no package.
		return obj.Name()
	}
	pkgPath := obj.Pkg().Path()
	switch o := obj.(type) {
	case *types.Func:
		sig, _ := o.Type().(*types.Signature)
		if sig != nil && sig.Recv() != nil {
			return qualifyMethod(sig.Recv().Type(), o.Name())
		}
		return pkgPath + "." + o.Name()
	default:
		return pkgPath + "." + obj.Name()
	}
}

func qualifyMethod(recv types.Type, name string) string {
	ptr, isPtr := recv.(*types.Pointer)
	if isPtr {
		recv = ptr.Elem()
	}
	// Strip generic instantiation: walk to the origin.
	named, ok := recv.(*types.Named)
	if !ok {
		// Aliases or other type forms — fall back to type string.
		return fmt.Sprintf("(%s).%s", recv.String(), name)
	}
	tn := named.Obj()
	if tn.Pkg() == nil {
		return fmt.Sprintf("(%s).%s", tn.Name(), name)
	}
	recvStr := tn.Pkg().Path() + "." + tn.Name()
	if isPtr {
		recvStr = "*" + recvStr
	}
	return fmt.Sprintf("(%s).%s", recvStr, name)
}

// QualifyNamed returns "pkgpath.TypeName" for a named type.
func QualifyNamed(named *types.Named) string {
	tn := named.Obj()
	if tn.Pkg() == nil {
		return tn.Name()
	}
	return tn.Pkg().Path() + "." + tn.Name()
}

// StripInstantiation drops `[...]` from a qualified name so input forms like
// `pkg.Foo[int]` collapse to `pkg.Foo`. The matching logic uses origin types.
func StripInstantiation(qn string) string {
	if before, _, ok := strings.Cut(qn, "["); ok {
		return before
	}
	return qn
}

// stripTestSuffix removes the "[pkg.test]" decoration applied by
// packages.Load when Tests is true.
func stripTestSuffix(pkgPath string) string {
	if before, _, ok := strings.Cut(pkgPath, " ["); ok {
		return before
	}
	return pkgPath
}

// buildSymbolTable walks every loaded package's top-level scope and methods,
// emitting one Sym per logical symbol. tier maps pkg path → tier and stamps
// each Sym so downstream filters are cheap; on QName collision the
// higher-priority tier wins (workspace > direct > indirect > stdlib).
func buildSymbolTable(fset *token.FileSet, pkgs []*packages.Package, tier map[string]PkgTier) *SymbolTable {
	st := &SymbolTable{
		BySN: make(map[string][]*Sym),
		ByQN: make(map[string]*Sym),
	}
	seenNamed := make(map[*types.Named]bool)

	for _, pkg := range pkgs {
		if pkg.Types == nil {
			continue
		}
		pkgTier := tier[pkg.PkgPath]
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			st.addObject(fset, obj, pkgTier)
			if tn, ok := obj.(*types.TypeName); ok {
				if named, ok := tn.Type().(*types.Named); ok {
					if !seenNamed[named] {
						seenNamed[named] = true
						st.AllNamed = append(st.AllNamed, named)
					}
					// Methods (declared in this package) are emitted as their own symbols.
					for m := range named.Methods() {
						st.addObject(fset, m, pkgTier)
					}
				}
			}
		}
	}
	return st
}

func (st *SymbolTable) addObject(fset *token.FileSet, obj types.Object, tier PkgTier) {
	if obj == nil || !obj.Exported() && obj.Pkg() == nil {
		return
	}
	kind := kindOf(obj)
	if kind == "" {
		return
	}
	qn := QualifyObject(obj)
	pos := fset.Position(obj.Pos())
	sym := &Sym{
		Kind:    kind,
		QName:   qn,
		SName:   obj.Name(),
		Obj:     obj,
		PkgPath: pkgPathOf(obj),
		Pos:     pos,
		Tier:    tier,
	}
	// On collision: prefer the higher-tier entry (workspace > direct > ...).
	// This also keeps the existing rule that `[pkg.test]` companions don't
	// clobber their real pkg, since both share the same tier and the first
	// wins ties.
	if existing, ok := st.ByQN[qn]; ok {
		if tier <= existing.Tier {
			return
		}
		// Replace BySN entry too — find and overwrite.
		bySN := st.BySN[sym.SName]
		for i, s := range bySN {
			if s == existing {
				bySN[i] = sym
				break
			}
		}
		st.ByQN[qn] = sym
		return
	}
	st.ByQN[qn] = sym
	st.BySN[sym.SName] = append(st.BySN[sym.SName], sym)
}

func kindOf(obj types.Object) SymKind {
	switch o := obj.(type) {
	case *types.Func:
		if sig, _ := o.Type().(*types.Signature); sig != nil && sig.Recv() != nil {
			return KindMethod
		}
		return KindFunc
	case *types.TypeName:
		return KindType
	case *types.Var:
		return KindVar
	case *types.Const:
		return KindConst
	default:
		return ""
	}
}

func pkgPathOf(obj types.Object) string {
	if obj.Pkg() == nil {
		return ""
	}
	return stripTestSuffix(obj.Pkg().Path())
}
