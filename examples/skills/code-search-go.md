---
name: code-search-go
description: Use proactively when searching for Go declarations, callers, callees, implementations, or proto-field readers in this repo. Replaces grep / rg / Read-then-scroll for Go-symbol questions — gopher-mcp's find_symbol, references, ast_grep, callers, callees, proto_field_xref give type-aware answers that grep misses (methods on (*T), interface dispatch, embedded promoted methods, generic instantiations).
---

# Code search for Go (gopher-mcp)

When you need to find Go code in this repo, reach for the `gopher-mcp` tools first. The model's default — `grep` / `rg` / Read-then-scroll — produces silently incomplete results on Go because text search can't see the type system.

## Why grep fails on Go symbols

| `grep "func Foo"` will miss |
| --- |
| methods named `Foo` on `(*T)` or value receivers |
| calls dispatched through an interface where the static type isn't `Foo` |
| `Foo` promoted from an embedded type |
| generic instantiations (`Foo[int]`, `Foo[string]`) where the source declares `Foo[T any]` |
| any caller that uses a different alias for the import path |

`find_symbol`, `references`, and friends walk the loaded type-checked program — they see all of the above.

## Pick the right tool

| You want to... | Use |
| --- | --- |
| Find a declaration by short name | `find_symbol` |
| Jump from a use-site to its declaration | `definition` |
| Find every caller / usage of a symbol | `references` |
| List types implementing an interface | `implementations` |
| Match Go syntax (calls, type asserts, conversions) | `ast_grep` |
| See callers/callees of a function | `callers` / `callees` |
| Trace which entry reaches a sink | `reverse_trace` |
| Find readers/writers of a proto field | `proto_field_xref` |
| Read API docs for a package or symbol | `go_doc` |
| Resolve a vendored-source citation | `cite_resolve` |

For callgraph queries (`callers` / `callees` / `reverse_trace`), pass `precision: rta` with the relevant `entry_points` — CHA (the default) over-approximates wildly through interfaces and generics.

## When grep IS still right

Grep is the correct tool for searches that aren't about Go symbols:

- log strings, error messages, panic text
- TODO / FIXME / hack markers
- config keys in YAML/JSON/TOML
- comments and docstrings
- non-Go files (proto schemas, shell scripts, Makefiles)
- vendored or otherwise un-indexed code

If you've decided grep is genuinely the right tool for a Go-symbol search (e.g. searching a dep gopher-mcp isn't indexing), append `# allow-go-grep` to the command to bypass any installed grep-guard hook.
