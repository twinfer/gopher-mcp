package index

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/ast/astutil"

	"github.com/twinfer/gopher-mcp/internal/util"
)

// Reference is one use-site of a symbol.
type Reference struct {
	File string
	Line int
	Col  int
}

// FindSymbols returns every symbol whose short name matches `name` (which
// supports '*' wildcards), optionally filtered by kind.
func (s *Snapshot) FindSymbols(name string, kind SymKind) []*Sym {
	if name == "" {
		return nil
	}
	var out []*Sym
	keep := func(sym *Sym) {
		if kind == "" || sym.Kind == kind {
			out = append(out, sym)
		}
	}
	if !strings.ContainsRune(name, '*') {
		for _, sym := range s.Syms.BySN[name] {
			keep(sym)
		}
		return out
	}
	re := util.CompileNameGlob(name)
	for short, syms := range s.Syms.BySN {
		if !re.MatchString(short) {
			continue
		}
		for _, sym := range syms {
			keep(sym)
		}
	}
	return out
}

// Definition resolves the symbol at file:line:col and returns its declaration
// position (which may be the same file or a different one).
func (s *Snapshot) Definition(absFile string, line, col int) (*Sym, error) {
	entry, ok := s.FileEntry(absFile)
	if !ok {
		return nil, fmt.Errorf("file not in any loaded package: %s", absFile)
	}
	pos, err := posAt(s.Fset, entry.AstFile, line, col)
	if err != nil {
		return nil, err
	}
	path, _ := astutil.PathEnclosingInterval(entry.AstFile, pos, pos)
	for _, n := range path {
		id, ok := n.(*ast.Ident)
		if !ok {
			continue
		}
		obj := entry.Pkg.TypesInfo.ObjectOf(id)
		if obj == nil {
			continue
		}
		qn := QualifyObject(obj)
		if sym, ok := s.Syms.ByQN[qn]; ok {
			return sym, nil
		}
		// Object exists but isn't in our top-level table (local var, package
		// builtin, etc.); synthesize a Sym from the Object directly.
		defPos := s.Fset.Position(obj.Pos())
		return &Sym{
			Kind:    kindOf(obj),
			QName:   qn,
			SName:   obj.Name(),
			Obj:     obj,
			PkgPath: pkgPathOf(obj),
			Pos:     defPos,
		}, nil
	}
	return nil, fmt.Errorf("no symbol at %s:%d:%d", absFile, line, col)
}

// References returns every use-site of the symbol named qname. scope selects
// which package tiers to walk (empty == workspace + direct). The second
// return reports whether the result was truncated by limit (0 = no limit).
func (s *Snapshot) References(qname, packageGlob string, scope Scope, limit int) ([]Reference, bool) {
	qname = StripInstantiation(qname)
	sym, ok := s.Syms.ByQN[qname]
	if !ok {
		return nil, false
	}
	target := sym.Obj
	var refs []Reference
	for _, pkg := range s.PkgsForScope(scope) {
		if pkg.TypesInfo == nil {
			continue
		}
		if !util.MatchPackagePath(packageGlob, pkg.PkgPath) {
			continue
		}
		for id, obj := range pkg.TypesInfo.Uses {
			if obj == target {
				pos := s.Fset.Position(id.Pos())
				refs = append(refs, Reference{File: pos.Filename, Line: pos.Line, Col: pos.Column})
				if limit > 0 && len(refs) >= limit {
					return refs, true
				}
			}
		}
	}
	return refs, false
}

// Implementations returns every named type whose method set satisfies the
// interface named by ifaceQN. scope selects which package tiers to walk.
func (s *Snapshot) Implementations(ifaceQN, packageGlob string, scope Scope) []*Sym {
	ifaceQN = StripInstantiation(ifaceQN)
	sym, ok := s.Syms.ByQN[ifaceQN]
	if !ok {
		return nil
	}
	iface, ok := sym.Obj.Type().Underlying().(*types.Interface)
	if !ok {
		return nil
	}
	tierSet := tierSetFor(scope)
	var out []*Sym
	for _, named := range s.Syms.AllNamed {
		pkg := named.Obj().Pkg()
		if pkg == nil {
			continue
		}
		if !tierSet[s.Tier[pkg.Path()]] {
			continue
		}
		if !util.MatchPackagePath(packageGlob, pkg.Path()) {
			continue
		}
		if _, isIface := named.Underlying().(*types.Interface); isIface {
			continue
		}
		if !types.Implements(named, iface) && !types.Implements(types.NewPointer(named), iface) {
			continue
		}
		qn := QualifyNamed(named)
		if hit, ok := s.Syms.ByQN[qn]; ok {
			out = append(out, hit)
		}
	}
	return out
}

// posAt resolves a 1-based line/column into a token.Pos within f. Columns are
// byte offsets into the line (matching token.Position semantics).
func posAt(fset *token.FileSet, f *ast.File, line, col int) (token.Pos, error) {
	if line < 1 {
		return token.NoPos, fmt.Errorf("line must be >= 1")
	}
	if col < 1 {
		col = 1
	}
	tf := fset.File(f.Pos())
	if tf == nil {
		return token.NoPos, fmt.Errorf("file not in fset")
	}
	if line > tf.LineCount() {
		return token.NoPos, fmt.Errorf("line %d > file length %d", line, tf.LineCount())
	}
	return tf.LineStart(line) + token.Pos(col-1), nil
}

// AbsFile turns a possibly-relative path into the canonical form used by the
// file index. Callers should resolve against the repo root first.
func AbsFile(root, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(root, p))
}
