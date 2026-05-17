// Package cite resolves repo-relative citations (e.g. "crates/foo.rs:42") to
// vendored source via the .repo-mcp.yaml citation patterns.
package cite

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/twinfer/gopher-mcp/internal/config"
)

// Match describes the resolved citation.
type Match struct {
	Pattern     string // citation pattern that matched
	VendorRoot  string // configured vendor root (as-typed)
	File        string // absolute path to the resolved file
	Line        int    // 1-based line number
	LineText    string // the line at File:Line
	Context     string // optional multi-line context window
	ContextLine int    // 1-based line number of the first context line
}

// lineSuffix matches the trailing ":<line>" of a citation.
var lineSuffix = regexp.MustCompile(`:(\d+)$`)

// Resolve runs the citation through every configured pattern in order; the
// first match wins. repoRoot is used to resolve VendorRoot when relative.
func Resolve(citation string, citations []config.Citation, repoRoot string, contextLines int) (*Match, error) {
	citation = strings.TrimSpace(citation)
	if citation == "" {
		return nil, fmt.Errorf("citation is empty")
	}
	for _, c := range citations {
		re, err := regexp.Compile(c.Pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", c.Pattern, err)
		}
		if !re.MatchString(citation) {
			continue
		}
		path, line, err := splitPathLine(citation)
		if err != nil {
			return nil, err
		}
		vendorRoot := c.VendorRoot
		if !filepath.IsAbs(vendorRoot) {
			vendorRoot = filepath.Join(repoRoot, vendorRoot)
		}
		// The path portion is rooted relative to vendorRoot's parent
		// (citations look like "crates/foo.rs:42" and vendor_root is the
		// vendored repo). Try both layouts: vendor_root/path and
		// vendor_root/<first-segment-stripped>.
		abs, lineText, ctx, ctxStart, err := readLineFromCandidates(vendorRoot, path, line, contextLines)
		if err != nil {
			return nil, err
		}
		return &Match{
			Pattern:     c.Pattern,
			VendorRoot:  c.VendorRoot,
			File:        abs,
			Line:        line,
			LineText:    lineText,
			Context:     ctx,
			ContextLine: ctxStart,
		}, nil
	}
	return nil, fmt.Errorf("no citation pattern matched %q", citation)
}

func splitPathLine(citation string) (string, int, error) {
	m := lineSuffix.FindStringSubmatchIndex(citation)
	if m == nil {
		return "", 0, fmt.Errorf("citation %q: missing :<line> suffix", citation)
	}
	path := citation[:m[0]]
	line, _ := strconv.Atoi(citation[m[2]:m[3]])
	if line < 1 {
		return "", 0, fmt.Errorf("citation %q: line must be >= 1", citation)
	}
	return path, line, nil
}

// readLineFromCandidates tries a couple of layouts before giving up:
//
//  1. <vendorRoot>/<path>
//  2. <vendorRoot>/<path-with-first-segment-stripped>
//
// The second matches the common case where the citation includes a top-level
// dir name that is itself the vendor root.
func readLineFromCandidates(vendorRoot, path string, line, contextLines int) (string, string, string, int, error) {
	candidates := []string{filepath.Join(vendorRoot, path)}
	if i := strings.IndexByte(path, '/'); i > 0 {
		candidates = append(candidates, filepath.Join(vendorRoot, path[i+1:]))
	}
	for _, abs := range candidates {
		text, ctx, ctxStart, err := readLine(abs, line, contextLines)
		if err == nil {
			return abs, text, ctx, ctxStart, nil
		}
		if !os.IsNotExist(err) {
			return abs, "", "", 0, err
		}
	}
	return "", "", "", 0, fmt.Errorf("file not found under vendor_root %q for path %q", vendorRoot, path)
}

func readLine(path string, line, contextLines int) (string, string, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", 0, err
	}
	defer f.Close()

	start := max(line-contextLines, 1)
	end := line + contextLines

	var (
		target string
		ctx    strings.Builder
		ctxOK  bool
		n      int
	)
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 64*1024), 1024*1024)
	for scan.Scan() {
		n++
		if n == line {
			target = scan.Text()
		}
		if contextLines > 0 && n >= start && n <= end {
			if ctxOK {
				ctx.WriteByte('\n')
			}
			ctx.WriteString(scan.Text())
			ctxOK = true
		}
		if n > end && n >= line {
			break
		}
	}
	if err := scan.Err(); err != nil {
		return "", "", 0, fmt.Errorf("scan %s: %w", path, err)
	}
	if n < line {
		return "", "", 0, fmt.Errorf("%s has only %d line(s); citation pointed at line %d", path, n, line)
	}
	return target, ctx.String(), start, nil
}
