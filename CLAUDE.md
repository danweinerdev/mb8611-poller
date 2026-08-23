# Claude compatibility guide

Follow [AGENTS.md](AGENTS.md) as the canonical repository guide. The short
code-graph rule below is repeated intentionally for clients that load only
`CLAUDE.md`; it does not supersede `AGENTS.md`.

## Code-graph-first exploration

When code-graph skills and MCP tools are available, load the applicable skills,
confirm or build the repository index, then use structural code-graph queries
for symbols, call relationships, inheritance, dependencies, coupling, and
refactor impact **before** searching source with `rg` or `grep`. Fall back to
source-text search only when code graph is unavailable or insufficient; use it
directly for free text, logs, docs, config, generated files, and unsupported
languages. Direct reads of known files and filename globs are fine.

## Claude-specific reminders

- Treat the requested task and its stated verification as the boundary; report
  missing decisions or scope mismatches rather than making product decisions.
- Keep operational claims grounded in the checked-in Makefile, service unit,
  configuration example, and code.
