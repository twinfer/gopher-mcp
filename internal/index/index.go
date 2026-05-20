package index

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/tools/go/packages"

	"github.com/twinfer/gopher-mcp/internal/config"
)

// PkgTier classifies a loaded package relative to the workspace module.
// Iteration policies (symbol table build, references, ast_grep) use this to
// scope work without re-walking the import graph.
type PkgTier uint8

const (
	TierUnknown PkgTier = iota
	TierStdlib
	TierIndirect
	TierDirect
	TierWorkspace
)

// String returns a stable lowercase label for the tier — used in JSON output
// and config parsing.
func (t PkgTier) String() string {
	switch t {
	case TierWorkspace:
		return "workspace"
	case TierDirect:
		return "direct"
	case TierIndirect:
		return "indirect"
	case TierStdlib:
		return "stdlib"
	default:
		return "unknown"
	}
}

// Index is the process-wide code index. Tools call Snapshot() to grab the
// current immutable view, then operate against it without locks. Reload()
// builds a fresh Snapshot and atomically swaps the pointer.
type Index struct {
	root      string
	cfg       config.RepoConfig
	buildTags []string
	snap      atomic.Pointer[Snapshot]
	relMu     sync.Mutex
}

// Snapshot is an immutable view of the loaded codebase.
//
// Pkgs is the workspace set returned by packages.Load("./..."). AllPkgs is
// the transitive flatten of Pkgs ∪ their Imports, deduped by PkgPath. With
// our NeedDeps|NeedSyntax|NeedTypesInfo load mode, every entry in AllPkgs
// already has full type+syntax info — iterating it is the only cost.
type Snapshot struct {
	Fset       *token.FileSet
	Pkgs       []*packages.Package
	AllPkgs    []*packages.Package
	Tier       map[string]PkgTier // keyed by PkgPath
	ModulePath string
	Syms       *SymbolTable
	Loaded     time.Time
	LoadErrs   []packages.Error
	files      map[string]fileEntry

	// Lazy callgraph state, materialized on first call to callgraph().
	cgInitOnce sync.Once
	cgState    *callgraphState
}

// TierOf returns the tier for the given package path, or TierUnknown if the
// package is not in the snapshot.
func (s *Snapshot) TierOf(pkgPath string) PkgTier {
	return s.Tier[pkgPath]
}

// Scope selects which package tiers a query iterates. Empty string and
// "default" resolve to ScopeWorkspaceDirect for backward-friendly defaults.
type Scope string

const (
	ScopeWorkspace       Scope = "workspace"
	ScopeWorkspaceDirect Scope = "workspace+direct"
	ScopeAll             Scope = "all"
)

// ParseScope normalizes a user-supplied scope string. Empty input maps to
// the default (workspace+direct). Unknown values return an error.
func ParseScope(s string) (Scope, error) {
	switch s {
	case "", "default":
		return ScopeWorkspaceDirect, nil
	case string(ScopeWorkspace):
		return ScopeWorkspace, nil
	case string(ScopeWorkspaceDirect):
		return ScopeWorkspaceDirect, nil
	case string(ScopeAll):
		return ScopeAll, nil
	}
	return "", fmt.Errorf("unknown scope %q (want workspace, workspace+direct, or all)", s)
}

// tierSetFor returns the set of tiers a scope iterates.
func tierSetFor(s Scope) map[PkgTier]bool {
	switch s {
	case ScopeWorkspace:
		return map[PkgTier]bool{TierWorkspace: true}
	case ScopeAll:
		return map[PkgTier]bool{
			TierWorkspace: true,
			TierDirect:    true,
			TierIndirect:  true,
			TierStdlib:    true,
		}
	default: // ScopeWorkspaceDirect
		return map[PkgTier]bool{TierWorkspace: true, TierDirect: true}
	}
}

// PkgsForScope returns the indexed packages whose tier matches the scope.
// Iteration order matches AllPkgs (roots first, then BFS).
func (s *Snapshot) PkgsForScope(scope Scope) []*packages.Package {
	want := tierSetFor(scope)
	out := make([]*packages.Package, 0, len(s.AllPkgs))
	for _, p := range s.AllPkgs {
		if want[s.Tier[p.PkgPath]] {
			out = append(out, p)
		}
	}
	return out
}

type fileEntry struct {
	Pkg     *packages.Package
	AstFile *ast.File
}

// New constructs an Index. Call Reload() at least once before use.
func New(root string, cfg config.RepoConfig, buildTags []string) *Index {
	return &Index{root: root, cfg: cfg, buildTags: buildTags}
}

// Snapshot returns the current snapshot, or nil if Reload has never succeeded.
func (ix *Index) Snapshot() *Snapshot { return ix.snap.Load() }

// Reload reloads the codebase. Concurrent calls are serialized.
func (ix *Index) Reload(ctx context.Context) error {
	ix.relMu.Lock()
	defer ix.relMu.Unlock()

	pkgs, loadErrs, err := loadPackages(ctx, ix.root, ix.buildTags)
	if err != nil {
		return err
	}
	var fset *token.FileSet
	for _, p := range pkgs {
		if p.Fset != nil {
			fset = p.Fset
			break
		}
	}
	if fset == nil {
		fset = token.NewFileSet()
	}
	modulePath := workspaceModulePath(pkgs)
	allPkgs := flattenAllPkgs(pkgs)
	tier := classifyTiers(allPkgs, modulePath)

	active := activeTiersFromConfig(ix.cfg.DepIndex)
	indexed := filterByTier(allPkgs, tier, active)

	syms := buildSymbolTable(fset, indexed, tier)
	files := buildFileIndex(indexed)

	ix.snap.Store(&Snapshot{
		Fset:       fset,
		Pkgs:       pkgs,
		AllPkgs:    allPkgs,
		Tier:       tier,
		ModulePath: modulePath,
		Syms:       syms,
		Loaded:     time.Now(),
		LoadErrs:   loadErrs,
		files:      files,
	})
	return nil
}

func buildFileIndex(pkgs []*packages.Package) map[string]fileEntry {
	out := make(map[string]fileEntry)
	for _, p := range pkgs {
		// Syntax is parallel to CompiledGoFiles, not GoFiles.
		for i, f := range p.Syntax {
			if i >= len(p.CompiledGoFiles) {
				break
			}
			path := filepath.Clean(p.CompiledGoFiles[i])
			// Later-iterated packages (e.g. the [pkg.test] companion) overwrite
			// earlier; the test variant's TypesInfo is a superset of the regular's.
			out[path] = fileEntry{Pkg: p, AstFile: f}
		}
	}
	return out
}

// workspaceModulePath returns the module path of the first workspace package
// (Module.Main == true). Stdlib loads or odd configurations may return "".
func workspaceModulePath(pkgs []*packages.Package) string {
	for _, p := range pkgs {
		if p.Module != nil && p.Module.Main {
			return p.Module.Path
		}
	}
	return ""
}

// flattenAllPkgs walks pkgs + their transitive Imports, deduped by PkgPath.
// Stable order: roots first (Pkgs slice order), then deps in BFS order.
func flattenAllPkgs(roots []*packages.Package) []*packages.Package {
	seen := make(map[string]bool)
	var out []*packages.Package
	var queue []*packages.Package
	for _, p := range roots {
		if p == nil || seen[p.PkgPath] {
			continue
		}
		seen[p.PkgPath] = true
		out = append(out, p)
		queue = append(queue, p)
	}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, imp := range p.Imports {
			if imp == nil || seen[imp.PkgPath] {
				continue
			}
			seen[imp.PkgPath] = true
			out = append(out, imp)
			queue = append(queue, imp)
		}
	}
	return out
}

// classifyTiers builds the tier map from package metadata.
//
//	pkg.Module == nil                         → TierStdlib
//	pkg.Module.Main || path == modulePath     → TierWorkspace
//	pkg.Module.Indirect == false              → TierDirect
//	otherwise                                 → TierIndirect
//
// The Path == modulePath fallback handles the test-variant [pkg.test] case
// where Module.Main isn't set on the companion.
func classifyTiers(pkgs []*packages.Package, modulePath string) map[string]PkgTier {
	out := make(map[string]PkgTier, len(pkgs))
	for _, p := range pkgs {
		out[p.PkgPath] = tierOf(p, modulePath)
	}
	return out
}

func tierOf(p *packages.Package, modulePath string) PkgTier {
	if p.Module == nil {
		return TierStdlib
	}
	if p.Module.Main {
		return TierWorkspace
	}
	if modulePath != "" && p.Module.Path == modulePath {
		return TierWorkspace
	}
	if !p.Module.Indirect {
		return TierDirect
	}
	return TierIndirect
}

// activeTiersFromConfig translates DepIndexConfig into a tier set. Workspace
// is always active; direct defaults on; indirect and stdlib default off.
func activeTiersFromConfig(d config.DepIndexConfig) map[PkgTier]bool {
	out := map[PkgTier]bool{TierWorkspace: true}
	if d.DirectEnabled() {
		out[TierDirect] = true
	}
	if d.Indirect {
		out[TierIndirect] = true
	}
	if d.Stdlib {
		out[TierStdlib] = true
	}
	return out
}

// filterByTier returns the subset of pkgs whose tier is in active. Order is
// preserved.
func filterByTier(pkgs []*packages.Package, tier map[string]PkgTier, active map[PkgTier]bool) []*packages.Package {
	if len(active) == 0 {
		return nil
	}
	out := make([]*packages.Package, 0, len(pkgs))
	for _, p := range pkgs {
		if active[tier[p.PkgPath]] {
			out = append(out, p)
		}
	}
	return out
}

// FileEntry returns the (pkg, ast) for an absolute file path, if loaded.
func (s *Snapshot) FileEntry(absPath string) (fileEntry, bool) {
	e, ok := s.files[filepath.Clean(absPath)]
	return e, ok
}
