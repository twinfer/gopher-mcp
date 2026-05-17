package server

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	execTimeout = 30 * time.Second
	testTimeout = 5 * time.Minute

	// runTestOutputCap bounds how much test output we surface back to the
	// model. Go test output can be huge (verbose mode, race traces); the tail
	// usually carries the FAIL summary so we keep the head + tail.
	runTestOutputCap = 32 * 1024
)

type goDocIn struct {
	Path string `json:"path" jsonschema:"the package or symbol to document, e.g. 'fmt' or 'fmt.Println'"`
}

type goDocOut struct {
	Output string `json:"output"`
}

type goListIn struct{}

type goListOut struct {
	Output string `json:"output"`
}

type runTestIn struct {
	Run      string `json:"run,omitempty" jsonschema:"regex passed to 'go test -run'; empty runs all tests"`
	Packages string `json:"packages,omitempty" jsonschema:"package pattern (e.g. './internal/...' or 'example.com/foo/bar'); defaults to './...'"`
	Tags     string `json:"tags,omitempty" jsonschema:"comma-separated build tags passed as '-tags='"`
	Race     bool   `json:"race,omitempty" jsonschema:"enable the race detector"`
	Count    int    `json:"count,omitempty" jsonschema:"value for '-count'; 0 omits the flag (Go's default of 1 applies, but cached results may be reused)"`
	Verbose  bool   `json:"verbose,omitempty" jsonschema:"pass '-v' for per-test output"`
}

type runTestOut struct {
	ExitCode  int    `json:"exit_code"`
	Output    string `json:"output"`
	Truncated bool   `json:"truncated"`
}

func (s *Server) registerExecTools() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "go_doc",
		Description: "Run 'go doc -all <path>' in the repo root. Returns formatted API documentation without source noise.",
	}, s.handleGoDoc)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "go_list_modules",
		Description: "Run 'go list -m -json all' in the repo root. Returns the module dependency graph as a JSON stream.",
	}, s.handleGoListModules)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "run_test",
		Description: "Run 'go test' in the repo root. Optional regex filter ('run'), package pattern ('packages', default './...'), build tags, race detector, count, and -v. Output is capped (head + tail) at 32KiB.",
	}, s.handleRunTest)
}

func (s *Server) handleGoDoc(ctx context.Context, _ *mcp.CallToolRequest, in goDocIn) (*mcp.CallToolResult, goDocOut, error) {
	if strings.TrimSpace(in.Path) == "" {
		return nil, goDocOut{}, errors.New("path is required")
	}
	out, _ := runGo(ctx, s.root, "doc", "-all", in.Path)
	return textResult(out), goDocOut{Output: out}, nil
}

func (s *Server) handleGoListModules(ctx context.Context, _ *mcp.CallToolRequest, _ goListIn) (*mcp.CallToolResult, goListOut, error) {
	out, _ := runGo(ctx, s.root, "list", "-m", "-json", "all")
	return textResult(out), goListOut{Output: out}, nil
}

func (s *Server) handleRunTest(ctx context.Context, _ *mcp.CallToolRequest, in runTestIn) (*mcp.CallToolResult, runTestOut, error) {
	args := []string{"test"}
	if in.Verbose {
		args = append(args, "-v")
	}
	if in.Race {
		args = append(args, "-race")
	}
	if in.Count > 0 {
		args = append(args, fmt.Sprintf("-count=%d", in.Count))
	}
	if tags := strings.TrimSpace(in.Tags); tags != "" {
		args = append(args, "-tags="+tags)
	}
	if run := strings.TrimSpace(in.Run); run != "" {
		args = append(args, "-run", run)
	}
	pkg := strings.TrimSpace(in.Packages)
	if pkg == "" {
		pkg = "./..."
	}
	args = append(args, pkg)

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = s.root
	raw, err := cmd.CombinedOutput()
	text := strings.TrimRight(string(raw), "\n")
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		text += fmt.Sprintf("\n[gopher-mcp] go test timed out after %s", testTimeout)
	}
	clipped, truncated := clipOutput(text, runTestOutputCap)

	exitCode := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return textResult(clipped), runTestOut{ExitCode: exitCode, Output: clipped, Truncated: truncated}, nil
}

// clipOutput keeps the head and tail of long text so the FAIL line at the
// bottom and the first failing test at the top both survive a cap.
func clipOutput(text string, cap int) (string, bool) {
	if len(text) <= cap {
		return text, false
	}
	half := cap / 2
	head := text[:half]
	tail := text[len(text)-half:]
	return head + "\n\n[... truncated " + fmt.Sprintf("%d bytes ...]\n\n", len(text)-cap) + tail, true
}

// runGo runs `go <args...>` in dir with a hard timeout. Stdout+stderr are
// merged; the returned error is non-nil on non-zero exit but the combined
// output is still useful to surface to the LLM, so callers typically ignore
// the error and return the text.
func runGo(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	text := strings.TrimRight(string(out), "\n")
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		text += fmt.Sprintf("\n[gopher-mcp] command timed out after %s", execTimeout)
	}
	return text, err
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}
