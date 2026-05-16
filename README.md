# mb8611-poller

A Go daemon that polls a Motorola MB8611 cable modem over HNAP and ships DOCSIS
channel and connection metrics to InfluxDB and event-log entries to
Elasticsearch. It is built on the shared
[`go-monitor`](https://github.com/danweinerdev/go-monitor) library, which
provides the collection loop, logging, and backend wiring.

> Status: bootstrap. The CLI surface and `go-monitor` wiring are in place, but
> real HNAP collection and the InfluxDB/Elasticsearch backends arrive in later
> phases.

## Quickstart

```sh
make build && ./bin/mb8611-poller --echo --run-once --config config.yaml
```

`config.yaml` is accepted on the command line but is not consumed yet — config
loading lands in a later phase. The stub parses the flag and runs the
`go-monitor` loop once with echo output.

## Building

`go-monitor` is a **private** module, so any `go` command that touches the
module graph requires:

```sh
export GOPRIVATE=github.com/danweinerdev/*
```

The Makefile sets this automatically for every recipe, so `make build` /
`make test` / `make verify` work out of the box. A manual `go build ./...`
needs `GOPRIVATE` exported in your shell first.

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
