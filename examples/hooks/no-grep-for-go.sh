#!/usr/bin/env bash
# no-grep-for-go.sh
#
# Claude Code PreToolUse hook: blocks Bash invocations that look like Go-symbol
# searches via grep / rg / git grep, and nudges the model toward gopher-mcp's
# type-aware tools (find_symbol, references, ast_grep, callers, callees).
#
# Install:
#   1. Copy this file somewhere on disk and chmod +x it.
#   2. Add to .claude/settings.json (project) or ~/.claude/settings.json (user):
#        {
#          "hooks": {
#            "PreToolUse": [{
#              "matcher": "Bash",
#              "hooks": [
#                { "type": "command", "command": "/abs/path/to/no-grep-for-go.sh" }
#              ]
#            }]
#          }
#        }
#
# Bypass for a one-off: append "# allow-go-grep" to the Bash command. Useful
# for grepping vendored deps, log strings, or other non-symbol searches that
# happen to trip the heuristic.

set -euo pipefail

input="$(cat)"

# Parse {tool_name, tool_input.command} from the hook input JSON. Prefer jq;
# fall back to a best-effort grep-extract so the hook works on minimal images.
if command -v jq >/dev/null 2>&1; then
  tool_name="$(printf '%s' "$input" | jq -r '.tool_name // empty')"
  cmd="$(printf '%s' "$input" | jq -r '.tool_input.command // empty')"
else
  tool_name="$(printf '%s' "$input" | grep -oE '"tool_name"[[:space:]]*:[[:space:]]*"[^"]+"' | head -1 | sed -E 's/.*"([^"]+)"$/\1/')"
  cmd="$(printf '%s' "$input" | grep -oE '"command"[[:space:]]*:[[:space:]]*"[^"]+"' | head -1 | sed -E 's/.*"([^"]+)"$/\1/')"
fi

[[ "$tool_name" == "Bash" ]] || exit 0
[[ -n "$cmd" ]] || exit 0

# Explicit bypass: the model has acknowledged it really wants grep here.
[[ "$cmd" == *"# allow-go-grep"* ]] && exit 0

# Only flag grep / rg / git grep invocations.
if ! [[ "$cmd" =~ (^|[[:space:]|;\&\(])(grep|rg|git[[:space:]]+grep)([[:space:]]|$) ]]; then
  exit 0
fi

# Heuristics for "this pattern is a Go-symbol search."
#   func Foo(...)              -> plain function decl
#   func (r *Recv) Foo(...)    -> method decl
#   type Foo struct|interface  -> type decl
#   interface { ... }          -> interface literal
#   .Foo(                      -> method/function call
go_symbol_regex='(func[[:space:]]+(\([^)]*\)[[:space:]]+)?[A-Z]|type[[:space:]]+[A-Z][A-Za-z0-9_]*[[:space:]]+(struct|interface)|interface[[:space:]]*\{|\.[A-Z][A-Za-z0-9_]*\()'

if [[ "$cmd" =~ $go_symbol_regex ]]; then
  cat >&2 <<'MSG'
[no-grep-for-go] This looks like a Go-symbol search. grep silently misses:
  - methods on (*T) or value receivers
  - calls dispatched through an interface
  - methods promoted from embedded types
  - generic instantiations (Foo[int], Foo[string])

Use the gopher-mcp tools instead:
  find_symbol      locate a declaration by name
  references       every usage of a qualified symbol
  definition       jump from a use-site to its declaration
  implementations  types satisfying an interface
  ast_grep         structural pattern search (calls, asserts, conversions)
  callers/callees  call-graph edges (pass precision: rta + entry_points for accuracy)
  proto_field_xref readers/writers of a proto field

If you really need grep here (log strings, vendored code, config keys, non-Go files),
re-run the command with "# allow-go-grep" appended to bypass this hook.
MSG
  exit 2
fi

exit 0
