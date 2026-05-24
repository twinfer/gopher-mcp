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

## Get Claude Code to actually use it

Claude Code (and most MCP clients) default to `grep` for code search — it's
fast, always available, and the model has years of training on it. Without
explicit routing, the model will reach for `grep` even when a semantic tool
would give a strictly better answer. The MCP tool descriptions push back on
this, but the strongest lever is a project-level `CLAUDE.md`.

Drop the following block into the `CLAUDE.md` at the root of any repo where
you've wired up `gopher-mcp`:

```md
## Go navigation — use the `repo` MCP, not grep

For Go code in this repo, prefer the MCP tools over textual search:

| Goal                              | Use                                            | Not                          |
| --------------------------------- | ---------------------------------------------- | ---------------------------- |
| Find where a symbol is declared   | `mcp__repo__find_symbol`                       | `grep "func Foo"`            |
| Jump from a use-site to its decl  | `mcp__repo__definition`                        | reading the file             |
| Find every caller of a function   | `mcp__repo__references` / `callers`            | `grep -r "Foo("`             |
| List types implementing an iface  | `mcp__repo__implementations`                   | grep + guessing              |
| Match Go syntax (calls, asserts)  | `mcp__repo__ast_grep`                          | `grep`                       |
| Trace which entry reaches code X  | `mcp__repo__reverse_trace`                     | reading call sites manually  |
| Find readers/writers of a proto   | `mcp__repo__proto_field_xref`                  | `grep "FieldName"`           |
| Resolve a `crates/...:42` comment | `mcp__repo__cite_resolve`                      | walking vendor by hand       |

Grep is still the right tool for: comments, log strings, config files,
non-Go files, and anything outside the indexed module(s).

Scope: the symbol/reference tools accept `scope` (`workspace`, `workspace+direct`
(default), or `all`). The default catches calls inside your module and its
direct `require`s. Pass `scope: "all"` to reach into indirect deps and stdlib
when the server has been started with `-deps all` (or `dep_index.stdlib: true`).
```

Rename `repo` to whatever you called the server in `.mcp.json`. The exact
phrasing matters less than being prescriptive — "use X when Y" beats
"please consider using the MCP."

### Escalations

If the `CLAUDE.md` block isn't enough, two ready-to-copy artifacts ship
under `examples/`:

- **Skill** — [`examples/skills/code-search-go.md`](examples/skills/code-search-go.md).
  Drop into `.claude/skills/code-search-go.md` (project) or
  `~/.claude/skills/code-search-go.md` (user). Skills appear in the
  available-skills list every turn, so this nudge stays in attention even
  on long sessions where `CLAUDE.md` scrolls out. The skill's frontmatter
  description triggers on Go-symbol search intents specifically.

- **PreToolUse hook** — [`examples/hooks/no-grep-for-go.sh`](examples/hooks/no-grep-for-go.sh).
  Blocks Bash invocations of `grep` / `rg` / `git grep` whose pattern looks
  like a Go declaration or call (`func Foo`, `type T struct`, `.Foo(`,
  etc.) and prints a message redirecting to the MCP tools. Heavier hand
  than the skill — it intercepts at the moment of misuse rather than
  hoping the model remembers. Install:

  ```bash
  cp examples/hooks/no-grep-for-go.sh ~/.claude/hooks/no-grep-for-go.sh
  chmod +x ~/.claude/hooks/no-grep-for-go.sh
  ```

  Then in `.claude/settings.json` (project) or `~/.claude/settings.json`
  (user):

  ```json
  {
    "hooks": {
      "PreToolUse": [{
        "matcher": "Bash",
        "hooks": [
          { "type": "command", "command": "/absolute/path/to/no-grep-for-go.sh" }
        ]
      }]
    }
  }
  ```

  Escape hatch for legitimate non-symbol searches (log strings, vendored
  deps, config keys): append `# allow-go-grep` to the command.

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

All navigation tools (`find_symbol`, `references`, `implementations`) and
`ast_grep` accept an optional `scope`: `workspace`, `workspace+direct`
(default), or `all`. The default returns hits from your module plus its
direct `require`s; `all` extends into indirect deps and the standard
library if those tiers are indexed (see [dep_index](#dependency-indexing)).

- **`find_symbol`** — locate symbols by short name (supports `*` wildcards),
  filter by kind (`func`, `method`, `type`, `var`, `const`). Each hit
  includes the package tier (`workspace` / `direct` / `indirect` / `stdlib`).
- **`definition`** — resolve the symbol at `file:line:col` to its
  declaration. Works across all indexed packages including stdlib.
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

## Dependency indexing

By default gopher-mcp indexes your workspace module plus everything in its
`require` lines (direct deps). Indirect deps and the Go standard library
are opt-in — each multiplies the indexed-symbol count by an order of
magnitude. Pick what you need:

```bash
gopher-mcp -root . -deps workspace   # workspace only
gopher-mcp -root . -deps direct      # default
gopher-mcp -root . -deps stdlib      # workspace + direct + stdlib
gopher-mcp -root . -deps all         # everything transitively reachable
```

Or in `.repo-mcp.yaml`:

```yaml
dep_index:
  direct: true       # default; false to drop direct deps
  indirect: false    # transitive deps not in your `require` block
  stdlib: false      # fmt, encoding/json, ...
```

At runtime, the navigation tools take a per-call `scope` parameter
(`workspace`, `workspace+direct` (default), `all`) that further narrows
which indexed tiers are searched. Indexing controls what's *available*;
scope controls what each query *touches*.

## Configuration: `.repo-mcp.yaml`

See [`.repo-mcp.example.yaml`](.repo-mcp.example.yaml) for a fully
commented example. The schema:

```yaml
version: 1
dep_index:    # which package tiers to index (see "Dependency indexing")
  direct: true
  indirect: false
  stdlib: false
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
