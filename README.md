# ping_exporter_cfgmgr

HTTP configuration service for ping_exporter. It reads `example.yaml`, selects
hosts for each slave, and publishes exporter-compatible YAML. Use the updated
ping_exporter from the adjacent repository: older exporters do not support
top-level labels or overriding the built-in `target` label.

## Run

```sh
go build -o ping_exporter_cfgmgr .
./ping_exporter_cfgmgr \
  -config.path=example.yaml \
  -web.listen-address=:9430 \
  -config.refresh=1s
```

The flags above show their defaults. The polling interval must be positive.
The service stops gracefully on SIGINT or SIGTERM. The existing module requires
Go 1.27.3 or a compatible toolchain.

```sh
curl http://localhost:9430/config/CAN-CT163
curl http://localhost:9430/config/CAN-CTCN2
curl http://localhost:9430/healthz
```

`GET /config/{slave}` returns HTTP 200 and YAML for a configured slave; unknown
slaves return 404. HEAD is also supported. Other methods return 405. `/healthz`
reports process availability, not whether the latest file update was accepted.

The server provides plain HTTP without authentication. Deploy on a trusted
network, bind to loopback, or put it behind an authenticated HTTPS reverse proxy
before exposing configurations outside that network.

## Connect a Slave

On the CAN-CT163 node, run the rebuilt exporter with:

```sh
./ping_exporter \
  --config.path=http://CONFIG_SERVER:9430/config/CAN-CT163 \
  --config.refresh=5s
```

For CAN-CTCN2, change the URL suffix to `CAN-CTCN2`. The exporter still requires
its usual ICMP permissions. Existing ping, DNS, HTTP serving and pushmetrics
flags remain available.

## Selection and Labels

- `slaves[].name` identifies the URL's slave. Names must be unique.
- `targets[].slaves` selects which slaves receive that group's hosts.
- Only explicitly selected groups contribute hosts. `group_name` is not an
  automatically generated metric label.
- `slaves[].labels` becomes top-level `labels` in the generated configuration
  and applies to all exporter metrics, including `ping_up`.
- `hosts[].labels` is flattened alongside `host` in the generated target and
  applies only to that host's metrics. A host value overrides a global value
  with the same label name. Labels present only on other hosts have empty
  values in the exporter's shared target label schema.
- Configured `target`, `ip`, and `ip_version` labels override the exporter's
  default values without generating duplicate label names.
- Label names must match `[a-zA-Z_][a-zA-Z0-9_]*`. The `__` prefix, `host`, and
  `type` are reserved. Label values are strings.
- A slave name is not implicitly added as `instance`. Add `instance` explicitly
  to the slave's labels when needed to distinguish nodes.
- If pushmetrics extra labels are configured on the exporter, those command-line
  labels override the generated labels for pushed metrics only.
- Duplicate hosts with identical labels are deduplicated per slave. Conflicting
  labels for the same host are rejected, rather than picking a group arbitrarily.

With the included example, CAN-CT163 receives:

```yaml
labels:
  location: CN
targets:
  - host: 104.245.12.188
    target: xTom.SJC
  - host: 128.1.145.129
    target: Zenlayer.HKG
```

CAN-CTCN2 receives only `104.245.12.188`, with its own global labels. The
`target` label is a display value; the address in `host` is still what gets pinged.

## Configuration Updates

The service checks file contents every `-config.refresh` interval, including
files replaced through rename. All slave responses are compiled and validated
before the new snapshot becomes visible. Unchanged files are not reapplied.
Invalid YAML, unknown fields, duplicate names, unknown slave references,
conflicting host definitions, and file read errors leave the last valid snapshot
available and are logged. An invalid initial configuration prevents startup.
Write updates to a temporary file and atomically rename it over the source to
avoid exposing a partially written but syntactically valid configuration.

The exporter polls independently using its own `--config.refresh`. With the
example intervals, changes normally take up to about six seconds plus request
and target setup time. Global labels, target labels, and group membership can
change without restarting either process. Existing queued push batches retain
the labels and timestamps they had when collected.

A defined slave with no matching hosts receives `targets: []`, which the updated
exporter accepts at startup and on reload. This stops all its targets while
keeping `ping_up` available. To retire a running slave, first remove its group
assignments and wait for it to receive the empty list. Deleting its slave entry
returns 404; the exporter deliberately keeps its last valid configuration on
HTTP failures. A source must define at least one slave.

## Verify

```sh
go test -race ./...
go vet ./...
```