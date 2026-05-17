package index

import (
	"context"
	"go/ast"
	"go/token"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/tools/go/packages"

	"github.com/twinfer/gopher-mcp/internal/config"
)

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
type Snapshot struct {
	Fset     *token.FileSet
	Pkgs     []*packages.Package
	Syms     *SymbolTable
	Loaded   time.Time
	LoadErrs []packages.Error
	files    map[string]fileEntry

	// Lazy callgraph state, materialized on first call to callgraph().
	cgInitOnce sync.Once
	cgState    *callgraphState
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
	syms := buildSymbolTable(fset, pkgs)
	files := buildFileIndex(pkgs)

	ix.snap.Store(&Snapshot{
		Fset:     fset,
		Pkgs:     pkgs,
		Syms:     syms,
		Loaded:   time.Now(),
		LoadErrs: loadErrs,
		files:    files,
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

// FileEntry returns the (pkg, ast) for an absolute file path, if loaded.
func (s *Snapshot) FileEntry(absPath string) (fileEntry, bool) {
	e, ok := s.files[filepath.Clean(absPath)]
	return e, ok
}
