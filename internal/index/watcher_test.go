package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/twinfer/gopher-mcp/internal/config"
)

// TestWatcher_TriggersReloadOnNewFile copies the tiny fixture to a tempdir,
// boots a watcher with a short debounce, drops a new .go file into a package,
// and verifies the index picks up the new symbol within a bounded wait.
func TestWatcher_TriggersReloadOnNewFile(t *testing.T) {
	root := copyFixture(t, "../../test/fixtures/tiny")

	cfg, _, err := config.Load(root)
	require.NoError(t, err)
	ix := New(root, cfg, nil)
	require.NoError(t, ix.Reload(t.Context()))

	// Sanity: NewlyAddedThing is not yet defined.
	require.Empty(t, ix.Snapshot().Syms.BySN["NewlyAddedThing"])

	wt, err := NewWatcher(ix, 50*time.Millisecond, t.Logf)
	require.NoError(t, err)
	t.Cleanup(func() { _ = wt.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- wt.Run(ctx) }()

	// Give the watcher a beat to install dir watches before we touch files.
	time.Sleep(100 * time.Millisecond)

	// Write a new file into an existing package.
	newGo := filepath.Join(root, "pkg", "leaf", "added.go")
	require.NoError(t, os.WriteFile(newGo, []byte("package leaf\n\ntype NewlyAddedThing struct{}\n"), 0o644))

	// Wait for the symbol to show up. The watcher debounces ~50ms, then
	// packages.Load takes a beat; 5s is generous.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(ix.Snapshot().Syms.BySN["NewlyAddedThing"]) > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("watcher did not reload to include NewlyAddedThing within deadline")
}

// TestWatcher_IgnoresIrrelevantFiles touches a non-Go file and verifies no
// reload happens (Loaded timestamp doesn't change) — this is a cost-control
// check, not a correctness one.
func TestWatcher_IgnoresIrrelevantFiles(t *testing.T) {
	root := copyFixture(t, "../../test/fixtures/tiny")

	cfg, _, err := config.Load(root)
	require.NoError(t, err)
	ix := New(root, cfg, nil)
	require.NoError(t, ix.Reload(t.Context()))
	loadedAt := ix.Snapshot().Loaded

	wt, err := NewWatcher(ix, 50*time.Millisecond, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = wt.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go func() { _ = wt.Run(ctx) }()

	require.NoError(t, os.WriteFile(filepath.Join(root, "README.txt"), []byte("hello\n"), 0o644))
	time.Sleep(250 * time.Millisecond) // well past debounce
	require.Equal(t, loadedAt, ix.Snapshot().Loaded, "non-Go file should not trigger reload")
}

// copyFixture replicates the named fixture directory tree to a fresh tempdir.
// Returns the tempdir absolute path. Cleanup is registered via t.Cleanup.
func copyFixture(t *testing.T, src string) string {
	t.Helper()
	srcAbs, err := filepath.Abs(src)
	require.NoError(t, err)
	dst := t.TempDir()
	err = filepath.Walk(srcAbs, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcAbs, p)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, data, info.Mode().Perm())
	})
	require.NoError(t, err)
	return dst
}
