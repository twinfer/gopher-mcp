package index

import (
	"errors"
	"fmt"
	"go/token"
	"go/types"
	"slices"
	"sort"
	"sync"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/callgraph/rta"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// Precision selects between CHA (default) and RTA. CHA is sound but
// over-approximates wildly with generics and interfaces; RTA is precise but
// requires reachable entry points.
type Precision string

const (
	PrecisionCHA Precision = "cha"
	PrecisionRTA Precision = "rta"
)

// CallEdge is one (caller → callee) edge with the call-site location.
type CallEdge struct {
	CallerQN string
	CalleeQN string
	File     string
	Line     int
	Col      int
}

// callgraphState lazily builds SSA + a call graph for one Snapshot. Bound to
// the snapshot for its lifetime; never shared across reloads.
type callgraphState struct {
	snap *Snapshot

	progOnce  sync.Once
	prog      *ssa.Program
	funcsByQN map[string][]*ssa.Function
	progErr   error

	chaOnce sync.Once
	cha     *callgraph.Graph
}

// ensureProgram builds the SSA program and the QName → Function index once.
func (c *callgraphState) ensureProgram() error {
	c.progOnce.Do(func() {
		prog, _ := ssautil.Packages(c.snap.Pkgs, ssa.InstantiateGenerics)
		if prog == nil {
			c.progErr = errors.New("ssa: no buildable packages (typecheck errors)")
			return
		}
		prog.Build()
		c.prog = prog
		c.funcsByQN = indexFunctions(prog)
	})
	return c.progErr
}

// ensureCHA builds (or returns) the CHA call graph over the SSA program.
func (c *callgraphState) ensureCHA() (*callgraph.Graph, error) {
	if err := c.ensureProgram(); err != nil {
		return nil, err
	}
	c.chaOnce.Do(func() {
		c.cha = cha.CallGraph(c.prog)
	})
	return c.cha, nil
}

// buildRTA constructs an RTA call graph rooted at the given entry-point QNames.
// RTA results are not cached; entry points are part of the query.
//
// When a qname resolves to multiple SSA variants (e.g. regular + test-compiled
// for a package with _test.go files), every variant is used as a root so RTA
// reachability covers both call-site populations.
func (c *callgraphState) buildRTA(entryQNs []string) (*callgraph.Graph, []string, error) {
	if err := c.ensureProgram(); err != nil {
		return nil, nil, err
	}
	if len(entryQNs) == 0 {
		return nil, nil, errors.New("rta: at least one entry_point is required")
	}
	var (
		roots   []*ssa.Function
		missing []string
	)
	for _, qn := range entryQNs {
		fns := c.funcsByQN[StripInstantiation(qn)]
		if len(fns) == 0 {
			missing = append(missing, qn)
			continue
		}
		roots = append(roots, fns...)
	}
	if len(roots) == 0 {
		return nil, missing, fmt.Errorf("rta: no entry points resolved (missing: %v)", missing)
	}
	res := rta.Analyze(roots, true)
	if res == nil || res.CallGraph == nil {
		return nil, missing, errors.New("rta: analysis returned no call graph")
	}
	return res.CallGraph, missing, nil
}

// indexFunctions builds qname → []*ssa.Function over every source-declared
// function in the program. We walk Members (which include unexported and
// non-root funcs like `main`) and named-type method sets, rather than relying
// on ssautil.AllFunctions which is a reachability heuristic.
//
// One qname can map to multiple SSA functions: with packages.Load(Tests: true),
// a package that has _test.go files is loaded twice (regular + test-compiled),
// yielding two *ssa.Function for the same source-level function. Importers
// outside the test binary are linked against the regular variant; functions
// in _test.go files are linked against the test variant. Keeping both ensures
// callgraph queries see call sites from either side.
func indexFunctions(prog *ssa.Program) map[string][]*ssa.Function {
	out := make(map[string][]*ssa.Function)
	add := func(fn *ssa.Function) {
		if fn == nil || fn.Synthetic != "" {
			return
		}
		qn := fn.String()
		if slices.Contains(out[qn], fn) {
			return
		}
		out[qn] = append(out[qn], fn)
	}
	for _, p := range prog.AllPackages() {
		if p == nil || p.Pkg == nil {
			continue
		}
		for _, m := range p.Members {
			switch v := m.(type) {
			case *ssa.Function:
				add(v)
			case *ssa.Type:
				// Method set on both the named type and its pointer; SSA
				// stores them as distinct *ssa.Function entries.
				addMethodSet(prog, p.Pkg, v.Type(), add)
				addMethodSet(prog, p.Pkg, types.NewPointer(v.Type()), add)
			}
		}
	}
	// Also include functions ssautil.AllFunctions found that we missed (e.g.
	// instantiated generics with InstantiateGenerics on). Origin-form only.
	for fn := range ssautil.AllFunctions(prog) {
		if fn != nil && fn.Origin() != nil && fn.Origin() != fn {
			add(fn.Origin())
		}
	}
	return out
}

func addMethodSet(prog *ssa.Program, fromPkg *types.Package, t types.Type, add func(*ssa.Function)) {
	mset := prog.MethodSets.MethodSet(t)
	for sel := range mset.Methods() {
		fn := prog.MethodValue(sel)
		add(fn)
	}
	_ = fromPkg
}

// callgraph returns the lazy callgraph state for this snapshot, creating it
// on first use.
func (s *Snapshot) callgraph() *callgraphState {
	s.cgInitOnce.Do(func() {
		s.cgState = &callgraphState{snap: s}
	})
	return s.cgState
}

// Callers returns every incoming call edge to fn (qualified name), using the
// selected precision. With CHA the result may contain spurious edges from
// generic/interface over-approximation; with RTA, edges are precise but limited
// to functions reachable from entry_points.
//
// When the qname has multiple SSA variants (see indexFunctions), edges are
// unioned across variants and deduped by call site.
func (s *Snapshot) Callers(qname string, prec Precision, entryPoints []string) ([]CallEdge, error) {
	g, fns, _, err := s.lookupNodes(qname, prec, entryPoints)
	if err != nil {
		return nil, err
	}
	edges := collectEdges(s.Fset, g, fns, func(n *callgraph.Node) []*callgraph.Edge { return n.In })
	sortEdges(edges)
	return edges, nil
}

// Callees returns every outgoing call edge from fn (qualified name).
func (s *Snapshot) Callees(qname string, prec Precision, entryPoints []string) ([]CallEdge, error) {
	g, fns, _, err := s.lookupNodes(qname, prec, entryPoints)
	if err != nil {
		return nil, err
	}
	edges := collectEdges(s.Fset, g, fns, func(n *callgraph.Node) []*callgraph.Edge { return n.Out })
	sortEdges(edges)
	return edges, nil
}

// collectEdges unions edges across every SSA variant of a logical function
// and dedupes by call site.
func collectEdges(fset *token.FileSet, g *callgraph.Graph, fns []*ssa.Function, pick func(*callgraph.Node) []*callgraph.Edge) []CallEdge {
	type key struct {
		caller, callee, file string
		line, col            int
	}
	seen := make(map[key]bool)
	var out []CallEdge
	for _, fn := range fns {
		node := g.Nodes[fn]
		if node == nil {
			continue
		}
		for _, e := range pick(node) {
			ce := edgeFromSSA(fset, e)
			k := key{ce.CallerQN, ce.CalleeQN, ce.File, ce.Line, ce.Col}
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, ce)
		}
	}
	return out
}

// ReverseTrace finds a call path from any of entryPoints to target. Returns
// the first path discovered (order: entryPoints), or nil if none exists.
//
// Entry-point and target qnames may each resolve to multiple SSA variants;
// the search tries every (entry-variant → any target-variant) pair until a
// path is found.
func (s *Snapshot) ReverseTrace(target string, entryPoints []string, prec Precision) ([]CallEdge, error) {
	if len(entryPoints) == 0 {
		return nil, errors.New("entry_points is required")
	}
	cg := s.callgraph()
	var (
		g       *callgraph.Graph
		err     error
		missing []string
	)
	if prec == PrecisionRTA {
		g, missing, err = cg.buildRTA(entryPoints)
		_ = missing
	} else {
		g, err = cg.ensureCHA()
	}
	if err != nil {
		return nil, err
	}
	targetFns := cg.funcsByQN[StripInstantiation(target)]
	if len(targetFns) == 0 {
		return nil, fmt.Errorf("target not found: %s", target)
	}
	targetNodes := make(map[*callgraph.Node]bool)
	for _, tFn := range targetFns {
		if n := g.Nodes[tFn]; n != nil {
			targetNodes[n] = true
		}
	}
	if len(targetNodes) == 0 {
		return nil, nil
	}
	isEnd := func(n *callgraph.Node) bool { return targetNodes[n] }
	for _, ep := range entryPoints {
		for _, epFn := range cg.funcsByQN[StripInstantiation(ep)] {
			startNode := g.Nodes[epFn]
			if startNode == nil {
				continue
			}
			path := callgraph.PathSearch(startNode, isEnd)
			if path != nil {
				out := make([]CallEdge, 0, len(path))
				for _, e := range path {
					out = append(out, edgeFromSSA(s.Fset, e))
				}
				return out, nil
			}
		}
	}
	return nil, nil
}

// lookupNodes resolves qname → []ssa.Function (all SSA variants sharing that
// qname) and returns the chosen call graph.
func (s *Snapshot) lookupNodes(qname string, prec Precision, entryPoints []string) (*callgraph.Graph, []*ssa.Function, []string, error) {
	cg := s.callgraph()
	if err := cg.ensureProgram(); err != nil {
		return nil, nil, nil, err
	}
	fns := cg.funcsByQN[StripInstantiation(qname)]
	if len(fns) == 0 {
		return nil, nil, nil, fmt.Errorf("function not found in SSA program: %s", qname)
	}
	if prec == PrecisionRTA {
		g, missing, err := cg.buildRTA(entryPoints)
		return g, fns, missing, err
	}
	g, err := cg.ensureCHA()
	return g, fns, nil, err
}

func edgeFromSSA(fset *token.FileSet, e *callgraph.Edge) CallEdge {
	ce := CallEdge{
		CallerQN: qnameOfSSAFunc(e.Caller.Func),
		CalleeQN: qnameOfSSAFunc(e.Callee.Func),
	}
	if e.Site != nil {
		pos := fset.Position(e.Site.Pos())
		ce.File = pos.Filename
		ce.Line = pos.Line
		ce.Col = pos.Column
	}
	return ce
}

func qnameOfSSAFunc(fn *ssa.Function) string {
	if fn == nil {
		return "<root>"
	}
	return fn.String()
}

func sortEdges(edges []CallEdge) {
	sort.Slice(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if a.CalleeQN != b.CalleeQN {
			return a.CalleeQN < b.CalleeQN
		}
		if a.CallerQN != b.CallerQN {
			return a.CallerQN < b.CallerQN
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})
}
