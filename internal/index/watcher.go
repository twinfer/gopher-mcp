package index

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// DefaultDebounce is the quiet window after the last filesystem event before
// the watcher triggers a reload. Short enough to feel live, long enough to
// coalesce a multi-file save (gofmt, goimports, IDE save-all).
const DefaultDebounce = 300 * time.Millisecond

// Watcher reloads an Index in response to source-tree changes. Watch
// directories recursively under root, debounce events, and serialize reloads
// against the Index's own lock.
type Watcher struct {
	ix       *Index
	root     string
	debounce time.Duration
	logf     func(format string, args ...any) // nil → silent
	w        *fsnotify.Watcher
}

// NewWatcher constructs a watcher bound to ix. logf is optional (nil silences
// reload-success logs; load errors still surface through Reload).
func NewWatcher(ix *Index, debounce time.Duration, logf func(string, ...any)) (*Watcher, error) {
	if debounce <= 0 {
		debounce = DefaultDebounce
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("fsnotify: %w", err)
	}
	return &Watcher{
		ix:       ix,
		root:     ix.root,
		debounce: debounce,
		logf:     logf,
		w:        w,
	}, nil
}

// Close stops the underlying fsnotify watcher. Safe to call multiple times.
func (wt *Watcher) Close() error { return wt.w.Close() }

// Run watches the tree and reloads on relevant changes until ctx is canceled
// or the underlying watcher closes. Returns context.Canceled on clean shutdown.
func (wt *Watcher) Run(ctx context.Context) error {
	if err := wt.addTree(wt.root); err != nil {
		_ = wt.w.Close()
		return fmt.Errorf("watcher: initial walk: %w", err)
	}

	var (
		timer   *time.Timer
		timerC  <-chan time.Time
		pending bool
	)
	arm := func() {
		pending = true
		if timer == nil {
			timer = time.NewTimer(wt.debounce)
		} else {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(wt.debounce)
		}
		timerC = timer.C
	}

	for {
		select {
		case <-ctx.Done():
			_ = wt.w.Close()
			return ctx.Err()

		case ev, ok := <-wt.w.Events:
			if !ok {
				return io.EOF
			}
			// New directory created → start watching it too. Catches
			// `mkdir new/pkg && touch new/pkg/x.go` between scans.
			if ev.Op.Has(fsnotify.Create) {
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
					if shouldWatchDir(wt.root, ev.Name) {
						_ = wt.w.Add(ev.Name)
					}
				}
			}
			if !relevant(ev) {
				continue
			}
			arm()

		case err, ok := <-wt.w.Errors:
			if !ok {
				return io.EOF
			}
			wt.log("watcher: error: %v", err)

		case <-timerC:
			if !pending {
				continue
			}
			pending = false
			start := time.Now()
			if err := wt.ix.Reload(ctx); err != nil {
				if errors.Is(err, context.Canceled) {
					return err
				}
				wt.log("watcher: reload failed: %v", err)
				continue
			}
			snap := wt.ix.Snapshot()
			if snap != nil {
				wt.log("watcher: reloaded %d package(s) in %s; %d load error(s)",
					len(snap.Pkgs), time.Since(start).Round(time.Millisecond), len(snap.LoadErrs))
			}
		}
	}
}

func (wt *Watcher) log(format string, args ...any) {
	if wt.logf != nil {
		wt.logf(format, args...)
	}
}

// addTree walks root and registers every directory the watcher should follow.
// Skips vendor/, .git/, node_modules/, testdata/, and dotdirs (besides root).
func (wt *Watcher) addTree(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Don't abort the whole walk over one unreadable subtree.
			wt.log("watcher: walk: %v", err)
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && !shouldWatchDir(root, path) {
			return filepath.SkipDir
		}
		if err := wt.w.Add(path); err != nil {
			wt.log("watcher: add %s: %v", path, err)
		}
		return nil
	})
}

// shouldWatchDir reports whether a directory is worth watching. Excludes
// large/irrelevant subtrees common in Go repos.
func shouldWatchDir(_, dir string) bool {
	base := filepath.Base(dir)
	switch base {
	case "vendor", "node_modules", "testdata", ".git":
		return false
	}
	if strings.HasPrefix(base, ".") && base != "." {
		return false
	}
	return true
}

// relevant filters events to file changes that could affect the index:
// .go files (including _test.go) and the module manifest.
func relevant(ev fsnotify.Event) bool {
	// Chmod-only events don't affect content.
	if ev.Op == fsnotify.Chmod {
		return false
	}
	base := filepath.Base(ev.Name)
	if base == "go.mod" || base == "go.sum" {
		return true
	}
	if strings.HasSuffix(base, ".go") {
		// Editor temp files (Vim: ".x.go.swp", JetBrains: "x.go___jb_tmp___")
		// shouldn't trigger reload.
		if strings.HasPrefix(base, ".") || strings.Contains(base, "___jb_") {
			return false
		}
		return true
	}
	return false
}
