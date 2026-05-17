package server

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/twinfer/gopher-mcp/internal/config"
	"github.com/twinfer/gopher-mcp/internal/index"
)

const (
	Name    = "gopher-mcp"
	Version = "0.1.0"
)

// Server bundles the MCP server with the state its tool handlers need.
type Server struct {
	root string
	cfg  config.RepoConfig
	ix   *index.Index
	mcp  *mcp.Server
}

// New wires resources + tools onto a fresh MCP server. Call Run to start it.
// ix may be nil for P1-only servers; index-backed tools error out cleanly when
// the snapshot is missing.
func New(root string, cfg config.RepoConfig, ix *index.Index) *Server {
	s := &Server{
		root: root,
		cfg:  cfg,
		ix:   ix,
		mcp:  mcp.NewServer(&mcp.Implementation{Name: Name, Version: Version}, nil),
	}
	s.registerResources()
	s.registerExecTools()
	if ix != nil {
		s.registerNavTools()
		s.registerASTTools()
		s.registerCallgraphTools()
		s.registerExtTools()
	}
	return s
}

// Run serves until ctx is canceled or the transport closes.
func (s *Server) Run(ctx context.Context, t mcp.Transport) error {
	return s.mcp.Run(ctx, t)
}

// snapshot returns the current index snapshot or an error if none has been loaded.
func (s *Server) snapshot() (*index.Snapshot, error) {
	if s.ix == nil {
		return nil, errors.New("index not configured for this server")
	}
	snap := s.ix.Snapshot()
	if snap == nil {
		return nil, errors.New("index not yet loaded")
	}
	return snap, nil
}
