# gopher-mcp

A repo-portable MCP server for navigating Go codebases with type-aware
queries: definitions, references, implementations, callgraphs, AST search,
proto-field xrefs, citation resolution, and configurable lints.

`gopher-mcp` takes no compile-time dependency on your project. Drop a
`.repo-mcp.yaml` at the repo root to enable per-project resources, citation
sources, proto packages, and lint analyzers; without one, the server still
works as a generic Go nav.

## Install

```bash
go install github.com/twinfer/gopher-mcp/cmd/gopher-mcp@latest
```

The binary speaks MCP over stdio. Wire it into your client (e.g. Claude
Code) via `.mcp.json`:

```json
{
  "mcpServers": {
    "repo": {
      "command": "gopher-mcp",
      "env": { "REPO_ROOT": "/absolute/path/to/your/repo" }
    }
  }
}
```

Or invoke it directly:

```bash
gopher-mcp -root /path/to/repo
gopher-mcp -root /path/to/repo -tags=integration,wasm   # extra build tags
```

`gopher-mcp` requires a `go.mod` at the root.

## Tools

All tools return both human-readable text and structured JSON
(`StructuredContent`) suitable for programmatic consumption.

### Exec
- **`go_doc`** — `go doc` against a package, symbol, or selector.
- **`go_list_modules`** — `go list -m all` for module discovery.
- **`run_test`** — `go test` with optional `run` regex, `packages` pattern
  (default `./...`), `tags`, `race`, `count`, `verbose`. Returns exit code
  + combined output (capped at 32 KiB head+tail).

### Navigation
- **`find_symbol`** — locate symbols by short name (supports `*` wildcards),
  filter by kind (`func`, `method`, `type`, `var`, `const`).
- **`definition`** — resolve the symbol at `file:line:col` to its
  declaration.
- **`references`** — list every use-site of a qualified symbol; supports
  `package_glob` scoping and `limit`.
- **`implementations`** — every named type whose method set satisfies a
  given interface.

### AST search
- **`ast_grep`** — structural search with four predicates:
  `call` (qualified callee + optional `n_args`), `typeassert` and `conv`
  (qualified target type, `*pkg.T` for pointer), `implements`.

### Callgraph
- **`callers`** / **`callees`** — incoming/outgoing call edges for a
  qualified function. Default precision is CHA (sound but
  over-approximates with generics/interfaces); pass `precision: rta` with
  `entry_points` for precise results limited to reachable code.
- **`reverse_trace`** — shortest path from any of `entry_points` to
  `target`. Useful for "which entry reaches this code?".

### Extensions (require `.repo-mcp.yaml`)
- **`cite_resolve`** — resolve a citation string like
  `crates/foo/bar.rs:42` against configured vendor roots; returns the
  resolved file, line number, line text, and optional context window.
- **`proto_field_xref`** — given a proto message + field name (snake_case
  or PascalCase), find every Go reference to the generated struct field.
- **`lint`** — run configured analyzers via `golang.org/x/tools/go/analysis`
  and return diagnostics.

## Symbol naming

Qualified names match `ssa.Function.String()` exactly:

| Kind                  | Form                              |
|-----------------------|-----------------------------------|
| Package func          | `pkg/path.FuncName`               |
| Pointer-recv method   | `(*pkg/path.Recv).Method`         |
| Value-recv method     | `(pkg/path.Recv).Method`          |
| Type                  | `pkg/path.TypeName`               |
| Proto-generated field | `pkg/path.Message.Field`          |

Generics: input accepts either origin form (`pkg.Foo`) or instantiation
form (`pkg.Foo[int]`); both resolve to the origin.

## Configuration: `.repo-mcp.yaml`

See [`.repo-mcp.example.yaml`](.repo-mcp.example.yaml) for a fully
commented example. The schema:

```yaml
version: 1
resources:    # files exposed as MCP resources (URI repo:<path>)
  - path: CLAUDE.md
    title: ...
    description: ...
citations:    # regex patterns for vendored-source citations
  - pattern: 'crates/[\w/.-]+\.rs:\d+'
    vendor_root: ./vendor/restate
proto:        # Go packages generated from .proto
  - import: example.com/repo/proto/enginev1
lint:         # analyzers to run via the `lint` tool
  - import: github.com/twinfer/gopher-mcp/pkg/analyzers/bannedinscope
    config:
      banned: [time.Now, "math/rand.*"]
      scope_packages: [example.com/repo/pkg/sdk/...]
entry_points: # named function sets for graph queries
  apply_path:
    - example.com/repo/internal/engine.(*Partition).Update
```

All sections are optional.

## Built-in analyzers

`pkg/analyzers/` is the only exported package surface. Currently shipped:

- **`bannedinscope`** — forbids calls to qualified names matching a glob
  list within scoped packages. Designed for determinism guards
  (deterministic execution sandboxes, leader-only code paths, etc).

## Adding analyzers

Analyzers must be linked into the binary at compile time. The
`.repo-mcp.yaml` `import:` field is a registry key, not a dynamic Go
import.

To add an analyzer:

1. Write a factory matching `lintreg.Factory`:
   ```go
   func Factory(cfg map[string]any) (*analysis.Analyzer, error) { ... }
   ```
2. Register it in `init()`:
   ```go
   lintreg.Register("your.org/analyzers/yourname", Factory)
   ```
3. Blank-import it from `cmd/gopher-mcp/main.go` so the `init` runs.

`pkg/analyzers/bannedinscope/analyzer.go` is the reference implementation.

## Architecture

- **Index** holds a snapshot of the loaded codebase
  (`golang.org/x/tools/go/packages`). Snapshots are immutable; tools read
  via an `atomic.Pointer` swap. Reloads are atomic and lock-free for
  readers.
- **Watcher** (fsnotify) recursively watches the source tree and triggers
  a debounced reload (300ms) on `.go`, `go.mod`, or `go.sum` changes.
- **Callgraph** is lazy: SSA program builds on first call, then CHA on top.
  RTA is uncached because entry points are part of the query.
- **Resources** are sandboxed: only files declared in `resources:` are
  readable. Path traversal (`..`, absolute paths) is rejected.

## Caveats

- CHA over-approximates wildly with generics and interfaces. Use
  `precision: rta` + `entry_points` for precise callgraph queries.
- The proto xref tool scans `.pb.go` struct tags; it does not link in
  protobuf descriptors. This is on purpose (no compile-time dep on your
  proto packages).
- The watcher reloads on any `.go` / `go.mod` / `go.sum` change under the
  root, debounced 300ms. `vendor/`, `.git/`, `node_modules/`, `testdata/`,
  and dotdirs are skipped. Reload errors are logged to stderr; the prior
  snapshot remains served.

## License

TBD.
