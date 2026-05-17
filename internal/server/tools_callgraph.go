package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/twinfer/gopher-mcp/internal/index"
)

// --- shared types ---

type callEdgeHit struct {
	CallerQN string `json:"caller_qname"`
	CalleeQN string `json:"callee_qname"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Col      int    `json:"col,omitempty"`
}

// --- callers ---

type callersIn struct {
	QName       string   `json:"qname" jsonschema:"qualified function name; matches ssa.Function.String()"`
	Precision   string   `json:"precision,omitempty" jsonschema:"'cha' (default, sound but over-approximates with generics/interfaces) or 'rta' (precise; requires entry_points)"`
	EntryPoints []string `json:"entry_points,omitempty" jsonschema:"for precision=rta: root functions that must reach this code"`
}

type callersOut struct {
	Edges []callEdgeHit `json:"edges"`
}

// --- callees ---

type calleesIn = callersIn

type calleesOut struct {
	Edges []callEdgeHit `json:"edges"`
}

// --- reverse_trace ---

type reverseTraceIn struct {
	Target      string   `json:"target" jsonschema:"qualified function we want to reach"`
	EntryPoints []string `json:"entry_points" jsonschema:"qualified functions to search from; first one with a path wins"`
	Precision   string   `json:"precision,omitempty" jsonschema:"'cha' (default) or 'rta'"`
}

type reverseTraceOut struct {
	Path  []callEdgeHit `json:"path"`
	Found bool          `json:"found"`
}

// --- registration ---

func (s *Server) registerCallgraphTools() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "callers",
		Description: "List functions that call `qname`. " +
			"Default precision is CHA (sound but over-approximates with generics/interfaces). " +
			"Pass precision='rta' with entry_points for precise results, but RTA only sees code reachable from those roots.",
	}, s.handleCallers)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "callees",
		Description: "List functions called by `qname`. Same precision options as `callers`.",
	}, s.handleCallees)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "reverse_trace",
		Description: "Find a call path from any of `entry_points` to `target`. " +
			"Useful for answering 'which entry point reaches this code?'. " +
			"Returns the first path found (search order matches entry_points order).",
	}, s.handleReverseTrace)
}

// --- handlers ---

func (s *Server) handleCallers(_ context.Context, _ *mcp.CallToolRequest, in callersIn) (*mcp.CallToolResult, callersOut, error) {
	if strings.TrimSpace(in.QName) == "" {
		return nil, callersOut{}, errors.New("qname is required")
	}
	snap, err := s.snapshot()
	if err != nil {
		return nil, callersOut{}, err
	}
	prec, err := parsePrecision(in.Precision)
	if err != nil {
		return nil, callersOut{}, err
	}
	edges, err := snap.Callers(in.QName, prec, in.EntryPoints)
	if err != nil {
		return nil, callersOut{}, err
	}
	out := callersOut{Edges: toEdgeHits(edges)}
	return textResult(fmt.Sprintf("found %d caller(s) of %s", len(out.Edges), in.QName)), out, nil
}

func (s *Server) handleCallees(_ context.Context, _ *mcp.CallToolRequest, in calleesIn) (*mcp.CallToolResult, calleesOut, error) {
	if strings.TrimSpace(in.QName) == "" {
		return nil, calleesOut{}, errors.New("qname is required")
	}
	snap, err := s.snapshot()
	if err != nil {
		return nil, calleesOut{}, err
	}
	prec, err := parsePrecision(in.Precision)
	if err != nil {
		return nil, calleesOut{}, err
	}
	edges, err := snap.Callees(in.QName, prec, in.EntryPoints)
	if err != nil {
		return nil, calleesOut{}, err
	}
	out := calleesOut{Edges: toEdgeHits(edges)}
	return textResult(fmt.Sprintf("found %d callee(s) of %s", len(out.Edges), in.QName)), out, nil
}

func (s *Server) handleReverseTrace(_ context.Context, _ *mcp.CallToolRequest, in reverseTraceIn) (*mcp.CallToolResult, reverseTraceOut, error) {
	if strings.TrimSpace(in.Target) == "" {
		return nil, reverseTraceOut{}, errors.New("target is required")
	}
	if len(in.EntryPoints) == 0 {
		return nil, reverseTraceOut{}, errors.New("entry_points is required")
	}
	snap, err := s.snapshot()
	if err != nil {
		return nil, reverseTraceOut{}, err
	}
	prec, err := parsePrecision(in.Precision)
	if err != nil {
		return nil, reverseTraceOut{}, err
	}
	path, err := snap.ReverseTrace(in.Target, in.EntryPoints, prec)
	if err != nil {
		return nil, reverseTraceOut{}, err
	}
	out := reverseTraceOut{Path: toEdgeHits(path), Found: path != nil}
	var msg string
	if out.Found {
		msg = fmt.Sprintf("found path of %d edge(s) to %s", len(out.Path), in.Target)
	} else {
		msg = fmt.Sprintf("no path to %s from given entry points", in.Target)
	}
	return textResult(msg), out, nil
}

func parsePrecision(s string) (index.Precision, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "cha":
		return index.PrecisionCHA, nil
	case "rta":
		return index.PrecisionRTA, nil
	default:
		return "", fmt.Errorf("precision must be 'cha' or 'rta', got %q", s)
	}
}

func toEdgeHits(edges []index.CallEdge) []callEdgeHit {
	out := make([]callEdgeHit, 0, len(edges))
	for _, e := range edges {
		out = append(out, callEdgeHit{
			CallerQN: e.CallerQN,
			CalleeQN: e.CalleeQN,
			File:     e.File,
			Line:     e.Line,
			Col:      e.Col,
		})
	}
	return out
}
