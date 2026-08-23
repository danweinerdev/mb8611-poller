# Coding-agent guide

This is the canonical guide for coding agents working in this repository.
`CLAUDE.md` is a compatibility entry point only. Do not commit unless the user
explicitly asks for a commit.

## Required exploration order

**Use code graph first.** When code-graph skills and MCP tools are available:

1. Load the applicable code-graph skills.
2. Confirm that the repository index exists and is current, or build it.
3. Use code-graph structural queries before source-text search to understand
   symbols, call relationships, inheritance, dependencies, coupling, and
   refactor impact.

Only fall back to `rg`/`grep` searches of source code when code graph is
unavailable or insufficient. Use text search directly for free text, logs,
documentation, configuration, generated files, and unsupported languages.
Direct reads of known files and filename globs are always fine.

## Repository map and architecture

- `cmd/mb8611-poller`: Cobra CLI and application wiring.
- `internal/config`: YAML loading, environment overrides, defaults, and
  validation.
- `internal/hnap`: authenticated MB8611 HNAP client and test support.
- `internal/docsis`: parsers for modem status responses.
- `internal/collector`: serial collection, lockout cooldown, and metric
  mapping.
- `cmd/dump-fixtures` and `internal/hnap/hnaptest/testdata`: fixture capture
  and sanitized HNAP responses.
- `grafana/`, `index-template.json`, and `mb8611-poller.service`: operational
  artifacts that must stay consistent with behavior and documentation.

One collection cycle logs in and requests six status actions through
`GetMultipleHNAPs`: downstream and upstream channels, connection info,
software, startup sequence, and event log. Keep protocol handling in `hnap`,
parsing in `docsis`, and measurement mapping in `collector`; do not bypass
those contracts from command wiring.

## CLI and output contracts

- Cobra subcommands are `run` (continuous) and `run-once` (one collection).
  A subcommand is required.
- `--config` and `--echo` are persistent flags and work before or after the
  subcommand.
- `--echo` still contacts the modem, bypasses production backends, and writes
  collected line protocol to stdout. Keep diagnostics, configuration failures,
  and command errors on stderr; preserve exit-code behavior.
- Normal runs require at least one enabled, configured backend. Echo-mode
  validation deliberately does not require backend connection settings.

## Telemetry and backend invariants

The exact public measurements are `docsis_downstream`, `docsis_upstream`,
`docsis_modem`, and `docsis_event`. Preserve their documented tag names, field
names, and Go value types. Every metric in a poll shares the poll observation
time as its timestamp; event `raw_date` and `raw_time` are modem strings, not
timestamp replacements.

Default routing is a contract: downstream, upstream, and modem measurements go
to InfluxDB; events go to Elasticsearch. Routing lists are validated
allowlists. `elasticsearch.id_from_tag: doc_id` makes the event ID the
Elasticsearch `_id` and removes that tag from `_source`; custom ID tags need
template support. Do not casually route event text into InfluxDB or channel
measurements into the event index.

## Modem safety and runtime changes

The MB8611 can lock out authentication. On `ErrLockedOut`, the collector must
remain quiet for the configured positive `modem.lockout_backoff` (default six
minutes). Do not add retry loops, parallel polls, or restart advice that can
produce HNAP traffic during that cooldown. Preserve serial collection and the
single-session `UN-AUTH` recovery behavior.

`SIGHUP`/`systemctl reload` rereads YAML and environment overrides, but only
`interval` and `log_level` take effect live. Changes to `modem.*`, `hostname`,
`influxdb.*`, `elasticsearch.*`, or `routing.*` require restart because the
HNAP client, collector, backend connections, credentials, TLS state, document
ID behavior, and filters are not rebuilt by reload.

## Style, tests, and fixtures

- Follow existing Go style, package boundaries, error handling, and table-driven
  test patterns. Run the verification requested by the task, not an expanded
  suite by default.
- Add or update focused tests with behavior changes, especially collector
  schema/routing, configuration validation, CLI stdout/stderr, and HNAP edge
  cases.
- `make build` deliberately cross-builds a static Linux amd64 binary with CGO
  disabled. `make test` is race-enabled; `make verify` checks gofmt, vet, and
  race tests.
- Capture fixtures only with `make fixtures` and credentials supplied through
  the `MB8611_*` environment variables. The workflow runs
  `scripts/scrub-fixtures.sh`; inspect output for MAC addresses, serials,
  customer values, and other identifiers before retaining it.

## Documentation and deployment synchronization

Keep `README.md`, `config.yaml.example`, `mb8611-poller.service`, Grafana
artifacts, and `index-template.json` synchronized whenever a behavior or
deployment contract changes. Documentation must distinguish `run-once` the
subcommand from flags, state that echo output is stdout, and describe the
installed systemd unit as current rather than planned.

`.plans/` contains historical local planning material, not current executable
specification and not published-clone content. Consult it for context only;
do not treat it as a reason to change scope or documentation without checking
the active code and operational artifacts.
