# mb8611-poller

## Overview

`mb8611-poller` is a Go daemon for one Motorola MB8611 cable modem. It polls
the modem's HNAP interface, sends DOCSIS channel and modem metrics to
InfluxDB, and sends modem event-log entries to Elasticsearch. It uses
`go-monitor` for scheduling, logging, and backend delivery.

TLS verification is enabled for the modem by default. Set
`modem.insecure_tls: true` only as an explicit, trusted-local-network opt-in
when the modem's self-signed certificate cannot be verified.

## Quickstart

Copy the example, replace its placeholders, and use echo mode to inspect the
collected metrics without writing to either backend:

```sh
make build && cp config.yaml.example config.yaml && $EDITOR config.yaml && ./bin/mb8611-poller run-once --echo --config config.yaml
```

Use `mb8611-poller run` for the continuous polling loop or
`mb8611-poller run-once` for one collection and exit. The persistent `--echo`
flag still contacts the modem, but it does not connect to InfluxDB or
Elasticsearch; it prints collected metrics to stdout instead. Production runs
need at least one enabled, fully configured backend. A subcommand is required;
invoking the binary with flags alone exits with a usage error rather than
silently doing no work.

## Configuration

The daemon reads the file named by `--config` (default `config.yaml`). Both
commands accept `--config` and `--echo` before or after the subcommand. YAML
values are literal; shell-style environment interpolation is not supported.
Use the environment overrides below rather than placing secrets in YAML.

| Field | Purpose and default |
| --- | --- |
| `modem.host` | Required modem host or address, without the HNAP path. No default. |
| `modem.username` | HNAP username. Empty is permitted by the loader; MB8611 deployments normally use `admin`. |
| `modem.password` | Required HNAP password. |
| `modem.insecure_tls` | Disable modem TLS certificate verification. Defaults to `false`; opt in only for a trusted modem with an unverifiable self-signed certificate. |
| `modem.lockout_backoff` | Positive cooldown after modem lockout detection. Defaults to `6m`. |
| `interval` | Required positive polling interval, such as `60s`. |
| `hostname` | `host` tag on emitted metrics. Empty uses the operating-system hostname. |
| `log_level` | Required logger threshold: `debug`, `info`, `warn`, or `error`. |
| `influxdb.enabled` | Enable the InfluxDB backend. Defaults to `false`. |
| `influxdb.url` | InfluxDB server URL; required when InfluxDB is enabled. |
| `influxdb.token` | InfluxDB token; required when InfluxDB is enabled. |
| `influxdb.org` | InfluxDB organization; required when InfluxDB is enabled. |
| `influxdb.bucket` | InfluxDB bucket; required when InfluxDB is enabled. |
| `elasticsearch.enabled` | Enable the Elasticsearch backend. Defaults to `false`. |
| `elasticsearch.url` | Elasticsearch server URL; required when Elasticsearch is enabled. |
| `elasticsearch.index` | Destination index; required when Elasticsearch is enabled. |
| `elasticsearch.api_key` | Elasticsearch API key. When empty, both `username` and `password` are required instead. |
| `elasticsearch.username` | Elasticsearch basic-auth username; used only when no API key is supplied. |
| `elasticsearch.password` | Elasticsearch basic-auth password; required with `username` when no API key is supplied. |
| `elasticsearch.insecure_tls` | Disable Elasticsearch TLS verification. Defaults to `false`; enable only for a deliberately trusted self-signed deployment. |
| `elasticsearch.id_from_tag` | Tag used as the Elasticsearch document ID. Empty leaves ID selection to the backend; `doc_id` makes event writes idempotent and omits that tag from `_source`. |
| `routing.influxdb_include` | InfluxDB measurement allowlist. Empty or omitted uses `docsis_downstream`, `docsis_upstream`, and `docsis_modem`. |
| `routing.elasticsearch_include` | Elasticsearch measurement allowlist. Empty or omitted uses `docsis_event`. |

`config.yaml.example` contains every field and the recommended routing values.

## Telemetry schema and capture

Each poll authenticates to the modem and issues the six status actions through
HNAP's `GetMultipleHNAPs` method. Every emitted metric uses the poll observation
time as `@timestamp`; it is not the modem event's occurrence time. The current
measurement schemas are:

| Measurement | Tags | Fields |
| --- | --- | --- |
| `docsis_downstream` | `host`, `modem`, `channel_id`, `channel`, `modulation`, `lock_status` | `frequency_mhz` (`float64`), `power_dbmv` (`float64`), `snr_db` (`float64`), `corrected` (`uint64`), `uncorrected` (`uint64`) |
| `docsis_upstream` | `host`, `modem`, `channel_id`, `channel`, `channel_type`, `lock_status` | `frequency_mhz` (`float64`), `power_dbmv` (`float64`), `symb_rate_ksps` (`int`) |
| `docsis_modem` | `host`, `modem`, `mac`, `serial`, `boot_status`, `connectivity_status`, `security_status`, `firmware_version` | `uptime_seconds` (`float64`), `ds_freq_mhz` (`float64`) |
| `docsis_event` | `host`, `modem`, `severity`, `doc_id` | `message` (`string`), `raw_date` (`string`), `raw_time` (`string`) |

By default, `docsis_downstream`, `docsis_upstream`, and `docsis_modem` route to
InfluxDB, while `docsis_event` routes to Elasticsearch. With
`elasticsearch.id_from_tag: doc_id`, the event `doc_id` becomes the
Elasticsearch `_id` and is omitted from `_source`. When `id_from_tag` is empty
or names a custom tag, `doc_id` remains a normal tag/document property.
Event `raw_date` and `raw_time` are modem-provided strings with no timezone.

The modem also currently makes these values available but does not emit them as
metrics: network access; software specification, hardware, certificate, and
customer versions; and startup boot, configuration-file, connectivity,
downstream, and security comments and configuration-file status.

## Environment Variables

Non-empty environment values override YAML after it is parsed. A malformed
`MB8611_INTERVAL` is ignored and leaves the YAML interval in effect. The other
variables are used as supplied and then validated with the resulting config.

For systemd deployments, keep secrets in `/etc/mb8611-poller/env` with mode
`0600` and use the intended `EnvironmentFile=-/etc/mb8611-poller/env` unit
setting. The installed unit loads this file when it exists, so credentials stay
out of the YAML configuration.

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

There are no environment overrides for backend enablement, TLS settings,
lockout backoff, Elasticsearch document-ID selection, or routing.

## Backends

### InfluxDB

Set `influxdb.enabled: true` and provide its URL, organization, bucket, and
token. Grant the token write permission to the configured bucket; it does not
need broader read or administrative permissions. The default route sends only
DOCSIS channel and modem measurements to this backend.

### Elasticsearch

Set `elasticsearch.enabled: true` and provide URL, index, and either an API
key or a basic-auth username/password pair. The credential needs permission to
create and write documents to the configured index (including Bulk API writes);
grant only the index privileges required by the Elasticsearch deployment. API
keys are generally preferable to long-lived basic-auth passwords.

Set `elasticsearch.id_from_tag: doc_id` to use the event's stable ID for
idempotent writes. With the strict template, explicitly map any custom tag
named by `id_from_tag` before using it.

#### Pre-create ES index

Install the optional event index template before Elasticsearch creates the
index:

```sh
curl -X PUT https://es.lan:9200/_index_template/mb8611-events -H 'Content-Type: application/json' --data-binary @index-template.json
```

## Grafana dashboard

Import `grafana/mb8611-influxql-dashboard.json` through Grafana's **Import**
page and select the prompted InfluxDB datasource. The datasource must use the
InfluxQL query language, have the configured InfluxDB database or bucket
mapping available to Grafana, and retain the `docsis_downstream`,
`docsis_upstream`, and `docsis_modem` measurements for the dashboard's chosen
time range.

The dashboard provides host, modem, and channel variables; current modem
uptime and downstream frequency stats; downstream power, SNR, and codeword
rates; upstream power, frequency, and symbol rate; and modem uptime over time.
Events are routed to Elasticsearch by default and are therefore not included
in this InfluxDB dashboard.

## Routing

Each backend is wrapped by `MeasurementFilter`. Its include list is an
allowlist: exact names match exactly, and a trailing `*` matches a
case-sensitive prefix. `*` is valid only as the final character; leading,
middle, or multiple wildcards are configuration errors. An empty list uses the
daemon defaults above.

Keep `docsis_downstream`, `docsis_upstream`, and `docsis_modem` in InfluxDB and
`docsis_event` in Elasticsearch. Sending event messages to InfluxDB can inflate
cardinality; sending DOCSIS statistics to Elasticsearch clutters the event
index and wastes indexing resources.

## Operations

### systemd deployment

On the target host, create the dedicated system user before installing. The
install target deliberately does not create it so operators can choose a UID.
It installs the binary and unit, copies `config.yaml.example` to
`/etc/mb8611-poller/config.yaml` only when that file does not already exist,
sets existing or new config ownership to `root:mb8611-poller` with mode `0640`
so the service can read it without exposing it to other users, and reloads
systemd:

```sh
sudo useradd --system --user-group --no-create-home mb8611-poller
make build
sudo make install
# Set modem credentials and enable/configure at least one backend before start.
sudoedit /etc/mb8611-poller/config.yaml
# Alternatively, put secrets in /etc/mb8611-poller/env as described below.
sudo systemctl enable --now mb8611-poller
sudo journalctl -u mb8611-poller -f
```

Do not start the service with the installed `changeme` placeholders. Configure
the modem credentials and enable at least one backend first; `make install`
already runs `systemctl daemon-reload`. Run `make build` as your normal user
before elevating only the installation step; this avoids root-owned build
artifacts in the checkout.

For upgrades, build and install the new binary the same way, then restart the
already-running service so it loads the replacement executable:

```sh
make build
sudo make install
sudo systemctl restart mb8611-poller
```

Create `/etc/mb8611-poller/env` with mode `0600` for secrets such as
`MB8611_MODEM_PASSWORD`, `MB8611_INFLUXDB_TOKEN`, and `MB8611_ES_API_KEY`.
The `EnvironmentFile` prefix permits the service to start if that file is
absent, which is useful for initial `--echo` validation.

`make install` requires root (or `sudo`), and its `systemctl` command requires
a systemd host. On a development machine without either, do not run the
install target; validate directly instead:

```sh
./bin/mb8611-poller run-once --config config.yaml --echo
```

The systemd path is manually verified on the target deployment host, not in
CI.

### Reload and restart boundaries

After editing `log_level`, send `SIGHUP` (or, once the planned unit exists,
`systemctl reload mb8611-poller`). The daemon rereads YAML and environment
overrides, but only these runtime settings take effect without restart:

- `interval`
- `log_level`

Restart the daemon after changing **any** `modem.*`, `hostname`,
`influxdb.*`, `elasticsearch.*`, or `routing.*` value. In particular, reload
does not rebuild the HNAP client, collector tags/lockout settings, backend
connections or credentials, backend enablement, TLS settings, Elasticsearch
document-ID behavior, or measurement filters.

## Troubleshooting

### Modem lockout

An `hnap: modem is in lockout` error or a `modem lockout detected, entering
cooldown` log means the modem rejected the first login stage. The collector
makes no HNAP traffic for `modem.lockout_backoff` (six minutes by default), so
do not repeatedly restart or retry during that period. Verify credentials,
wait for the cooldown to expire, then correct the configuration and restart if
needed.

### UN-AUTH responses

`UN-AUTH` usually means another browser or client session displaced the
daemon's single modem admin session, or the modem rebooted and rotated its
session key. The daemon re-authenticates and retries once. Close competing
admin sessions and wait for a modem reboot to finish; persistent responses
also warrant checking the configured username and password.

### TLS failures

The modem's self-signed certificate may cause an unknown-authority or hostname
verification error. Prefer a verified certificate when possible. If that is
not possible on a trusted local network, explicitly set
`modem.insecure_tls: true` and restart; this disables certificate verification.
Likewise, keep `elasticsearch.insecure_tls` false unless a trusted
self-signed Elasticsearch deployment requires the explicit opt-in.

## Legacy

The repository-root `poller.py` is deprecated and retained for protocol
reference only; it is not a supported deployment path.

## Development

```sh
make build
make test
make lint
make verify
```

The architecture, HNAP polling approach, and metric/event mapping are in
`.plans/Designs/Mb8611Poller/README.md` in the local planning workspace. That
file is not included in published clones.
