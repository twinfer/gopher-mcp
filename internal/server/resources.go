package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// resourceScheme is an opaque URI prefix: repo:<relative-path>.
// Opaque (no //) keeps parsing trivial — no Host/Path ambiguity.
const resourceScheme = "repo:"

func (s *Server) registerResources() {
	for _, r := range s.cfg.Resources {
		title := r.Title
		if title == "" {
			title = r.Path
		}
		s.mcp.AddResource(&mcp.Resource{
			URI:         resourceScheme + r.Path,
			Name:        title,
			Description: r.Description,
			MIMEType:    mimeForPath(r.Path),
		}, s.readResource)
	}
}

func (s *Server) readResource(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	rel, ok := strings.CutPrefix(req.Params.URI, resourceScheme)
	if !ok {
		return nil, mcp.ResourceNotFoundError(req.Params.URI)
	}
	if !s.isDeclaredResource(rel) {
		return nil, mcp.ResourceNotFoundError(req.Params.URI)
	}
	abs, err := safeJoin(s.root, rel)
	if err != nil {
		return nil, mcp.ResourceNotFoundError(req.Params.URI)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, mcp.ResourceNotFoundError(req.Params.URI)
	}
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{{
			URI:      req.Params.URI,
			MIMEType: mimeForPath(rel),
			Text:     string(data),
		}},
	}, nil
}

func (s *Server) isDeclaredResource(rel string) bool {
	for _, r := range s.cfg.Resources {
		if r.Path == rel {
			return true
		}
	}
	return false
}

// safeJoin refuses absolute paths and any rel that escapes root.
func safeJoin(root, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute path not allowed: %s", rel)
	}
	cleaned := filepath.Clean(rel)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return "", fmt.Errorf("path escapes root: %s", rel)
	}
	return filepath.Join(root, cleaned), nil
}

func mimeForPath(p string) string {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".md":
		return "text/markdown"
	case ".yaml", ".yml":
		return "application/yaml"
	case ".json":
		return "application/json"
	case ".go":
		return "text/x-go"
	case ".proto":
		return "text/plain"
	default:
		return "text/plain"
	}
}
