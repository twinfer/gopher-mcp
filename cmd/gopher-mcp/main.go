package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/twinfer/gopher-mcp/internal/config"
	"github.com/twinfer/gopher-mcp/internal/index"
	"github.com/twinfer/gopher-mcp/internal/server"

	// Built-in analyzers — each registers itself via init().
	_ "github.com/twinfer/gopher-mcp/pkg/analyzers/bannedinscope"
)

func main() {
	var (
		rootFlag string
		tagsFlag string
	)
	flag.StringVar(&rootFlag, "root", "", "repository root (defaults to $REPO_ROOT or cwd)")
	flag.StringVar(&tagsFlag, "tags", "", "comma-separated build tags for packages.Load")
	flag.Parse()

	root, err := resolveRoot(rootFlag)
	if err != nil {
		log.Fatalf("gopher-mcp: %v", err)
	}

	cfg, found, err := config.Load(root)
	if err != nil {
		log.Fatalf("gopher-mcp: config: %v", err)
	}
	if found {
		fmt.Fprintf(os.Stderr, "gopher-mcp: loaded .repo-mcp.yaml (v%d) from %s\n", cfg.Version, root)
	} else {
		fmt.Fprintf(os.Stderr, "gopher-mcp: no .repo-mcp.yaml in %s; running in generic mode\n", root)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var tags []string
	if tagsFlag != "" {
		tags = splitAndTrim(tagsFlag)
	}
	ix := index.New(root, cfg, tags)
	loadStart := time.Now()
	if err := ix.Reload(ctx); err != nil {
		log.Fatalf("gopher-mcp: index load: %v", err)
	}
	snap := ix.Snapshot()
	fmt.Fprintf(os.Stderr, "gopher-mcp: indexed %d package(s) in %s; %d load error(s)\n",
		len(snap.Pkgs), time.Since(loadStart).Round(time.Millisecond), len(snap.LoadErrs))

	wt, err := index.NewWatcher(ix, 0, func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "gopher-mcp: "+format+"\n", args...)
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "gopher-mcp: watcher disabled: %v\n", err)
	} else {
		go func() {
			if err := wt.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				fmt.Fprintf(os.Stderr, "gopher-mcp: watcher: %v\n", err)
			}
		}()
		defer wt.Close()
	}

	srv := server.New(root, cfg, ix)

	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("gopher-mcp: server: %v", err)
	}
}

func splitAndTrim(csv string) []string {
	var out []string
	for s := range strings.SplitSeq(csv, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func resolveRoot(flagVal string) (string, error) {
	root := flagVal
	if root == "" {
		root = os.Getenv("REPO_ROOT")
	}
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("cwd: %w", err)
		}
		root = cwd
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	if _, err := os.Stat(filepath.Join(abs, "go.mod")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("no go.mod in %s; gopher-mcp requires a Go module root", abs)
		}
		return "", fmt.Errorf("stat go.mod: %w", err)
	}
	return abs, nil
}
