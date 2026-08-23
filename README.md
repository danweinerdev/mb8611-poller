# mb8611-poller

A Go daemon that polls a Motorola MB8611 cable modem over HNAP and ships DOCSIS
channel and connection metrics to InfluxDB and event-log entries to
Elasticsearch. It is built on the shared
[`go-monitor`](https://github.com/danweinerdev/go-monitor) library, which
provides the collection loop, logging, and backend wiring.

> Status: active development. HNAP collection and the routed
> InfluxDB/Elasticsearch backend wiring are available.

## Quickstart

```sh
make build && ./bin/mb8611-poller --echo --run-once --config config.yaml
```

The daemon reads the YAML configuration named by `--config`; environment
variables with the `MB8611_` prefix override its values. `--echo` writes the
normally routed metrics to standard output instead of connecting to InfluxDB
or Elasticsearch.

YAML values are literal: `${VAR}` shell-style interpolation is not supported.
Set the documented `MB8611_*` environment variables to override configuration.

TLS certificate verification is enabled by default. MB8611 firmware commonly
uses a self-signed certificate, so set `modem.insecure_tls: true` only on a
trusted local network when verification cannot succeed.

## Configuration reloads

Send `SIGHUP` to reload the YAML configuration and apply environment
overrides. Only `interval` and `log_level` take effect in the running daemon.
Changes to `modem.*`, `influxdb.*`, `elasticsearch.*`, or `routing.*` require
a daemon restart; existing HNAP and backend connections are not rebuilt on
reload.

## Building

`go-monitor` is a public module; a plain `go build ./...` / `go test ./...`
resolves it through the default Go module proxy with no extra configuration.

Common targets:

- `make build` — static Linux binary into `bin/`
- `make test` — `go test -v -race ./...`
- `make lint` — `golangci-lint run` if available, else `go vet ./...`
- `make verify` — aggregate gate: gofmt check, `go vet`, race tests
- `make clean` — remove build artifacts
- `make install` — install the binary to `/usr/local/bin`

## Design

The architecture, HNAP polling approach, and metric/event mapping are
documented in the design at `.plans/Designs/Mb8611Poller/README.md`. That file
lives in the project's local planning workspace and is intentionally not part
of the published repository, so it will not be present in a clone.
