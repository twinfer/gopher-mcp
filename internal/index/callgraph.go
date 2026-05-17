package index

import (
	"errors"
	"fmt"
	"go/token"
	"go/types"
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

	progOnce sync.Once
	prog     *ssa.Program
	funcByQN map[string]*ssa.Function
	progErr  error

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
		c.funcByQN = indexFunctions(prog)
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
func (c *callgraphState) buildRTA(entryQNs []string) (*callgraph.Graph, []string, error) {
	if err := c.ensureProgram(); err != nil {
		return nil, nil, err
	}
	if len(entryQNs) == 0 {
		return nil, nil, errors.New("rta: at least one entry_point is required")
	}
	roots := make([]*ssa.Function, 0, len(entryQNs))
	var missing []string
	for _, qn := range entryQNs {
		fn, ok := c.funcByQN[StripInstantiation(qn)]
		if !ok || fn == nil {
			missing = append(missing, qn)
			continue
		}
		roots = append(roots, fn)
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

// indexFunctions builds qname → *ssa.Function over every source-declared
// function in the program. We walk Members (which include unexported and
// non-root funcs like `main`) and named-type method sets, rather than relying
// on ssautil.AllFunctions which is a reachability heuristic.
func indexFunctions(prog *ssa.Program) map[string]*ssa.Function {
	out := make(map[string]*ssa.Function)
	add := func(fn *ssa.Function) {
		if fn == nil || fn.Synthetic != "" {
			return
		}
		qn := fn.String()
		if _, exists := out[qn]; !exists {
			out[qn] = fn
		}
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
func (s *Snapshot) Callers(qname string, prec Precision, entryPoints []string) ([]CallEdge, error) {
	g, fn, _, err := s.lookupNode(qname, prec, entryPoints)
	if err != nil {
		return nil, err
	}
	node := g.Nodes[fn]
	if node == nil {
		return nil, nil
	}
	out := make([]CallEdge, 0, len(node.In))
	for _, e := range node.In {
		out = append(out, edgeFromSSA(s.Fset, e))
	}
	sortEdges(out)
	return out, nil
}

// Callees returns every outgoing call edge from fn (qualified name).
func (s *Snapshot) Callees(qname string, prec Precision, entryPoints []string) ([]CallEdge, error) {
	g, fn, _, err := s.lookupNode(qname, prec, entryPoints)
	if err != nil {
		return nil, err
	}
	node := g.Nodes[fn]
	if node == nil {
		return nil, nil
	}
	out := make([]CallEdge, 0, len(node.Out))
	for _, e := range node.Out {
		out = append(out, edgeFromSSA(s.Fset, e))
	}
	sortEdges(out)
	return out, nil
}

// ReverseTrace finds a call path from any of entryPoints to target. Returns
// the first path discovered (order: entryPoints), or nil if none exists.
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
	targetFn, ok := cg.funcByQN[StripInstantiation(target)]
	if !ok {
		return nil, fmt.Errorf("target not found: %s", target)
	}
	targetNode := g.Nodes[targetFn]
	if targetNode == nil {
		return nil, nil
	}
	isEnd := func(n *callgraph.Node) bool { return n == targetNode }
	for _, ep := range entryPoints {
		epFn, ok := cg.funcByQN[StripInstantiation(ep)]
		if !ok {
			continue
		}
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
	return nil, nil
}

// lookupNode resolves qname → ssa.Function and returns the chosen call graph.
func (s *Snapshot) lookupNode(qname string, prec Precision, entryPoints []string) (*callgraph.Graph, *ssa.Function, []string, error) {
	cg := s.callgraph()
	if err := cg.ensureProgram(); err != nil {
		return nil, nil, nil, err
	}
	fn, ok := cg.funcByQN[StripInstantiation(qname)]
	if !ok {
		return nil, nil, nil, fmt.Errorf("function not found in SSA program: %s", qname)
	}
	if prec == PrecisionRTA {
		g, missing, err := cg.buildRTA(entryPoints)
		return g, fn, missing, err
	}
	g, err := cg.ensureCHA()
	return g, fn, nil, err
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
