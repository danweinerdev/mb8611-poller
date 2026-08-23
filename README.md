# mb8611-poller

`mb8611-poller` is a Go daemon for a Motorola MB8611 cable modem. It polls the
modem's HNAP interface, publishes DOCSIS channel and modem telemetry to
InfluxDB, and publishes modem event-log entries to Elasticsearch. Scheduling,
logging, and backend delivery come from `go-monitor`.

## Overview and features

- Collects six MB8611 status responses through HNAP in one polling cycle.
- Emits stable downstream, upstream, modem, and event measurements.
- Routes DOCSIS statistics to InfluxDB and event logs to Elasticsearch by
  default.
- Supports a safe `--echo` mode that prints collected line protocol rather
  than connecting to production backends.
- Includes a Grafana InfluxQL dashboard, an optional Elasticsearch index
  template, fixture capture tooling, and a systemd unit.

TLS verification is enabled for the modem by default. Set
`modem.insecure_tls: true` only as an explicit trusted-local-network opt-in
when the modem's self-signed certificate cannot be verified.

## Prerequisites

- Go **1.26.5** for local development (`go.mod` declares module
  `github.com/danweinerdev/mb8611-poller`).
- A reachable MB8611 and its HNAP credentials.
- InfluxDB and/or Elasticsearch for production collection. At least one
  enabled, fully configured backend is required outside echo mode.
- A Linux amd64 target for `make build`: the Makefile builds with
  `CGO_ENABLED=0`, `GOOS=linux`, and `GOARCH=amd64`. Build another platform
  with an explicit `go build` invocation instead.

## Quick start

Build, create a local configuration, replace the placeholders, and validate
collection without writing to either backend:

```sh
make build
cp config.yaml.example config.yaml
$EDITOR config.yaml
./bin/mb8611-poller run-once --echo --config config.yaml
```

`--echo` still contacts the modem, but writes the collected line protocol to
stdout instead of connecting to InfluxDB or Elasticsearch. Do not use the
installed `changeme` placeholders against a modem.

## CLI

The Cobra CLI requires a subcommand; supplying flags to the binary alone exits
with a usage error rather than silently doing no work.

| Command | Behavior |
| --- | --- |
| `mb8611-poller run` | Start the continuous polling loop. |
| `mb8611-poller run-once` | Collect once and exit. |

Both commands accept these persistent flags before or after the subcommand:

| Flag | Default | Behavior |
| --- | --- | --- |
| `--config PATH` | `config.yaml` | Read configuration from `PATH`. |
| `--echo` | `false` | Print collected line protocol to stdout rather than publishing to production backends. |

For example:

```sh
./bin/mb8611-poller --config config.yaml run-once --echo
./bin/mb8611-poller run --config /etc/mb8611-poller/config.yaml
```

## Configuration

The daemon parses YAML from `--config`, then applies non-empty environment
overrides. YAML values are literal: shell-style environment interpolation is
not supported. Keep secrets in environment variables rather than YAML where
possible.

| Field | Purpose and default |
| --- | --- |
| `modem.host` | Required modem host or address, without the HNAP path. |
| `modem.username` | HNAP username. The loader permits empty; MB8611 deployments normally use `admin`. |
| `modem.password` | Required HNAP password. |
| `modem.insecure_tls` | Disable modem certificate verification. Defaults to `false`; use only for a trusted modem with an unverifiable self-signed certificate. |
| `modem.lockout_backoff` | Positive cooldown after lockout detection. Defaults to `6m`. |
| `interval` | Required positive polling interval, for example `60s`. |
| `hostname` | `host` tag on emitted metrics. Empty uses the operating-system hostname. |
| `log_level` | Required threshold: `debug`, `info`, `warn`, or `error`. |
| `influxdb.enabled` | Enable InfluxDB. Defaults to `false`. |
| `influxdb.url`, `token`, `org`, `bucket` | Required when InfluxDB is enabled. |
| `elasticsearch.enabled` | Enable Elasticsearch. Defaults to `false`. |
| `elasticsearch.url`, `index` | Required when Elasticsearch is enabled. |
| `elasticsearch.api_key` | API key; when empty, both `username` and `password` are required. |
| `elasticsearch.username`, `password` | Basic-auth credentials used only without an API key. |
| `elasticsearch.insecure_tls` | Disable Elasticsearch certificate verification. Defaults to `false`; use only for a deliberately trusted self-signed deployment. |
| `elasticsearch.id_from_tag` | Tag to use as the document ID. `doc_id` makes event writes idempotent and omits that tag from `_source`; empty leaves ID selection to the backend. |
| `routing.influxdb_include` | InfluxDB measurement allowlist. Empty or omitted defaults to `docsis_downstream`, `docsis_upstream`, and `docsis_modem`. |
| `routing.elasticsearch_include` | Elasticsearch measurement allowlist. Empty or omitted defaults to `docsis_event`. |

`config.yaml.example` contains every field and the recommended routing values.
The include lists are allowlists: exact names match exactly, and a trailing
`*` matches a case-sensitive prefix. `*` is valid only as the final character;
leading, middle, or multiple wildcards are configuration errors.

### Environment overrides

Non-empty environment values override YAML after parsing. A malformed
`MB8611_INTERVAL` is ignored, leaving the YAML interval in effect; the other
values are used as supplied and then validated. There are no environment
overrides for backend enablement, TLS, lockout backoff, Elasticsearch document
ID selection, or routing.

| Variable | Overrides |
| --- | --- |
| `MB8611_MODEM_HOST` | `modem.host` |
| `MB8611_MODEM_USERNAME` | `modem.username` |
| `MB8611_MODEM_PASSWORD` | `modem.password` |
| `MB8611_INFLUXDB_URL` | `influxdb.url` |
| `MB8611_INFLUXDB_TOKEN` | `influxdb.token` |
| `MB8611_INFLUXDB_ORG` | `influxdb.org` |
| `MB8611_INFLUXDB_BUCKET` | `influxdb.bucket` |
| `MB8611_ES_URL` | `elasticsearch.url` |
| `MB8611_ES_INDEX` | `elasticsearch.index` |
| `MB8611_ES_API_KEY` | `elasticsearch.api_key` |
| `MB8611_ES_USERNAME` | `elasticsearch.username` |
| `MB8611_ES_PASSWORD` | `elasticsearch.password` |
| `MB8611_INTERVAL` | `interval` |
| `MB8611_HOSTNAME` | `hostname` |
| `MB8611_LOG_LEVEL` | `log_level` |

## Architecture and layout

Each poll authenticates to HNAP and requests these six actions through
`GetMultipleHNAPs`: downstream channels, upstream channels, connection info,
software, startup sequence, and event log. `internal/hnap` implements the
client and authentication; `internal/docsis` parses modem responses;
`internal/collector` maps parsed data to metrics; `internal/config` loads and
validates configuration; and `cmd/mb8611-poller` owns CLI wiring and backend
construction.

| Path | Contents |
| --- | --- |
| `cmd/mb8611-poller` | Cobra commands and application wiring. |
| `cmd/dump-fixtures` | Captures modem responses for tests. |
| `internal/hnap` | HNAP protocol client, authentication, and test server. |
| `internal/docsis` | DOCSIS parsers and parser tests. |
| `internal/collector` | Metric mapping, timestamps, and lockout handling. |
| `internal/config` | YAML, environment overrides, defaults, and validation. |
| `grafana/` | Importable InfluxQL dashboard. |
| `index-template.json` | Optional Elasticsearch event-index template. |
| `mb8611-poller.service` | Installed systemd service unit. |

## Telemetry, backends, and dashboard

Every metric from a poll uses the poll observation time as `@timestamp`; it
is not an event's modem occurrence time. The schema is:

| Measurement | Tags | Fields |
| --- | --- | --- |
| `docsis_downstream` | `host`, `modem`, `channel_id`, `channel`, `modulation`, `lock_status` | `frequency_mhz` (`float64`), `power_dbmv` (`float64`), `snr_db` (`float64`), `corrected` (`uint64`), `uncorrected` (`uint64`) |
| `docsis_upstream` | `host`, `modem`, `channel_id`, `channel`, `channel_type`, `lock_status` | `frequency_mhz` (`float64`), `power_dbmv` (`float64`), `symb_rate_ksps` (`int`) |
| `docsis_modem` | `host`, `modem`, `mac`, `serial`, `boot_status`, `connectivity_status`, `security_status`, `firmware_version` | `uptime_seconds` (`float64`), `ds_freq_mhz` (`float64`) |
| `docsis_event` | `host`, `modem`, `severity`, `doc_id` | `message` (`string`), `raw_date` (`string`), `raw_time` (`string`) |

By default, `docsis_downstream`, `docsis_upstream`, and `docsis_modem` route
to InfluxDB; `docsis_event` routes to Elasticsearch. Keep that split: event
messages can inflate InfluxDB cardinality, while DOCSIS statistics clutter and
waste resources in the event index. With `elasticsearch.id_from_tag: doc_id`,
the event `doc_id` becomes Elasticsearch `_id` and is omitted from `_source`.
If `id_from_tag` is empty or names a custom tag, `doc_id` remains a normal
tag/document property. `raw_date` and `raw_time` are modem-provided strings
without a timezone.

The modem exposes some values that are intentionally not emitted: network
access; software specification, hardware, certificate, and customer versions;
and startup boot, configuration-file, connectivity, downstream, and security
comments and configuration-file status.

### Backend setup

For InfluxDB, enable `influxdb` and provide its URL, organization, bucket, and
token. Grant that token write permission to the configured bucket; it needs no
broader read or administrative permission.

For Elasticsearch, enable `elasticsearch` and provide its URL, index, and an
API key or a basic-auth username/password pair. The credential needs only the
index privileges necessary to create and write documents, including Bulk API
writes. API keys are generally preferable to long-lived basic-auth passwords.
Before automatic index creation, optionally install the event template:

```sh
curl -X PUT https://es.lan:9200/_index_template/mb8611-events \
  -H 'Content-Type: application/json' --data-binary @index-template.json
```

With the strict template, explicitly map a custom tag named by `id_from_tag`
before using it.

### Grafana dashboard

Import `grafana/mb8611-influxql-dashboard.json` from Grafana's **Import** page
and select the prompted InfluxDB datasource. The datasource must use InfluxQL,
have the configured InfluxDB database or bucket mapping available, and retain
the three DOCSIS measurements for the dashboard's time range. The dashboard
provides host, modem, and channel variables; current modem uptime and
downstream frequency; downstream power, SNR, and codeword rates; upstream
power, frequency, and symbol rate; and modem uptime over time. Events are not
part of this dashboard because they route to Elasticsearch by default.

## Deployment and operations

### systemd deployment

The repository includes `mb8611-poller.service`. On a target host, create the
dedicated service user before installation; `make install` deliberately does
not choose a UID or create the user. The target installs the binary and unit,
copies `config.yaml.example` to `/etc/mb8611-poller/config.yaml` only when it
does not exist, sets that config to `root:mb8611-poller` mode `0640`, and runs
`systemctl daemon-reload`.

```sh
sudo useradd --system --user-group --no-create-home mb8611-poller
make build
sudo make install
# Configure credentials and enable at least one backend before starting.
sudoedit /etc/mb8611-poller/config.yaml
sudo systemctl enable --now mb8611-poller
sudo journalctl -u mb8611-poller -f
```

Build as your normal user and elevate only `make install` to avoid root-owned
checkout artifacts. `make install` needs root (or `sudo`) and a systemd host;
on a development machine without either, validate with `--echo` instead. For
an upgrade, rebuild and reinstall, then restart the already-running service:

```sh
make build
sudo make install
sudo systemctl restart mb8611-poller
```

For systemd, keep secrets in `/etc/mb8611-poller/env` with mode `0600`, for
example `MB8611_MODEM_PASSWORD`, `MB8611_INFLUXDB_TOKEN`, and
`MB8611_ES_API_KEY`. The installed unit has
`EnvironmentFile=-/etc/mb8611-poller/env`, so a missing environment file does
not itself block startup; the resulting configuration must still be valid.
Before starting the service, use a transient unit to perform `--echo`
validation with that same environment file without copying its values into a
shell command:

```sh
sudo systemd-run --wait --pipe --collect \
  --property=EnvironmentFile=/etc/mb8611-poller/env \
  /usr/local/bin/mb8611-poller run-once \
  --config /etc/mb8611-poller/config.yaml --echo
```

The systemd path is manually verified on the target host, not in CI.

### Reload and restart boundaries

After changing `log_level`, send `SIGHUP` or run:

```sh
sudo systemctl reload mb8611-poller
```

Reload rereads YAML and environment overrides, but only `interval` and
`log_level` take effect without restart. Restart after changing any
`modem.*`, `hostname`, `influxdb.*`, `elasticsearch.*`, or `routing.*` value.
Reload does not rebuild the HNAP client, collector tags or lockout settings,
backend connections or credentials, backend enablement, TLS settings,
Elasticsearch document-ID behavior, or measurement filters.

## Troubleshooting

### Modem lockout

`hnap: modem is in lockout` or `modem lockout detected, entering cooldown`
means the modem rejected the first login stage. The collector makes no HNAP
traffic for `modem.lockout_backoff` (six minutes by default). Do not repeatedly
restart or retry during the cooldown. Verify credentials, wait for it to
expire, then correct configuration and restart if needed.

### `UN-AUTH` responses

`UN-AUTH` usually means another browser or client displaced the daemon's
single modem admin session, or the modem rebooted and rotated its session key.
The daemon re-authenticates and retries once. Close competing admin sessions,
wait for a reboot to finish, and check the configured credentials if it
persists.

### TLS failures

A modem self-signed certificate can cause unknown-authority or hostname
verification errors. Prefer a verified certificate. Only on a trusted local
network, set `modem.insecure_tls: true` and restart to disable verification.
Likewise, leave `elasticsearch.insecure_tls` false unless a trusted self-signed
Elasticsearch deployment requires the explicit opt-in.

## Development and testing

```sh
make build   # Linux amd64, CGO disabled
go build ./... # compile all packages for the local platform
make test    # verbose race-enabled Go tests
make lint    # golangci-lint, or go vet when it is unavailable
make verify  # gofmt check, go vet, and race tests
```

To refresh HNAP fixtures, use a reachable modem and supply credentials only
through the environment:

```sh
MB8611_MODEM_HOST=... MB8611_MODEM_USERNAME=... MB8611_MODEM_PASSWORD=... make fixtures
```

The target writes fixtures to `internal/hnap/hnaptest/testdata` and then runs
`scripts/scrub-fixtures.sh` to remove device and customer identifiers. Review
the scrubbed changes before committing them. Historical design and plan
material under `.plans/` records how the current implementation was produced;
it is not the current user or developer guide.
