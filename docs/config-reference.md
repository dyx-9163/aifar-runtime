# AIFAR Runtime Configuration

Runtime configuration is loaded from YAML with `aifar-runtime serve --config /etc/aifar-runtime/config.yaml`.
CLI flags such as `--listen` and `--state-dir` are only operational overrides; new runtime parameters should be added to this file format first.

## Fields

| Field | Default | Purpose |
| --- | --- | --- |
| `api.listen` | `127.0.0.1:18081` | Runtime HTTP API listen address. |
| `api.readHeaderTimeout` | `10s` | Header read timeout for the Runtime API. |
| `api.shutdownTimeout` | `30s` | Maximum graceful shutdown time for the API and proxy listeners. |
| `api.maxRequestBytes` | `4194304` | Maximum rendered Runtime request body size accepted by mutating API endpoints. |
| `node.name` | `local` | Local node identity. Runtime `spec.nodeName` must match this value when set. |
| `node.labels` | empty | Local node labels used by Runtime `spec.nodeSelector`. |
| `node.capacity` | empty | Local node capacity used by Scheduler Lite admission. Empty resource fields are not enforced. |
| `node.allocatable` | `node.capacity` | Local allocatable resources used by Scheduler Lite. Defaults to `capacity` when empty. |
| `state.backend` | `file` | State backend. `etcd` is reserved for the future clustered control plane and is rejected until implemented. |
| `state.dir` | `/var/lib/aifar-runtime` | Persistent specs, statuses, events, and proxy route state. |
| `state.etcd.endpoints` | empty | Reserved etcd endpoints for future clustered storage. |
| `state.etcd.prefix` | `/aifar-runtime` | Reserved etcd key prefix. |
| `state.etcd.dialTimeout` | `5s` | Reserved etcd dial timeout. |
| `docker.command` | `docker` | Container CLI used by the runtime. |
| `docker.restartPolicy` | `unless-stopped` | Restart policy applied to managed containers. |
| `docker.addHost` | `host.docker.internal:host-gateway` | Host mapping injected into managed containers. |
| `docker.eventDebounce` | `2s` | Debounce window after Docker events before resync. |
| `docker.eventBackoff` | `5s` | Backoff after Docker event watcher failures. |
| `container.readyTimeout` | `5m` | Maximum time to wait for a container to become ready. |
| `container.readyPollInterval` | `3s` | Poll interval while waiting for container readiness. |
| `container.diagnosticsLogTail` | `120` | Number of container log lines captured on readiness failure. |
| `container.httpHealthCheckTemplate` | `wget -qO- http://127.0.0.1:%d%s >/dev/null` | Docker health check command template. It must include `%d` for port and `%s` for path. |
| `selfHeal.enabled` | `true` | Enables automatic replacement of exited or unhealthy managed containers during resync. |
| `selfHeal.maxRestarts` | `3` | Maximum restart attempts tracked per managed container before the Runtime stays `Degraded`. |
| `selfHeal.backoff` | `10s` | Base restart backoff. Each retry waits `attempt * backoff`. |
| `proxy.readHeaderTimeout` | `10s` | Header read timeout for Service and Ingress proxy listeners. |
| `reconcile.interval` | `30s` | Periodic full reconciliation interval. |
| `health.dockerTimeout` | `5s` | Docker readiness check timeout. |
| `security.bearerToken` | empty | Optional Runtime API bearer token. Prefer `bearerTokenFile` in production. |
| `security.bearerTokenFile` | empty | File containing the Runtime API bearer token. |
| `security.tlsCertFile` | empty | Optional TLS certificate file for the Runtime API. Must be set with `tlsKeyFile`. |
| `security.tlsKeyFile` | empty | Optional TLS key file for the Runtime API. Must be set with `tlsCertFile`. |
| `security.rbac.enabled` | `false` | Enables lightweight token RBAC. Do not combine with `bearerToken` or `bearerTokenFile`. |
| `security.rbac.tokens[].name` | empty | Token principal name. |
| `security.rbac.tokens[].role` | `admin` | `admin`, `operator`, or `viewer`. |
| `security.rbac.tokens[].tokenFile` | empty | File containing the principal token. Prefer this over inline `token`. |
| `observability.metricsEnabled` | `true` | Enables the Prometheus-compatible `/metrics` endpoint. |
| `audit.enabled` | `true` | Enables JSONL audit logging for mutating API calls and local backup/restore operations. |
| `audit.path` | `/var/log/aifar-runtime/audit.jsonl` | Audit log file path. |
| `audit.maxFileSize` | `10485760` | Maximum audit log file size in bytes before internal rotation. |
| `audit.maxBackups` | `5` | Number of rotated audit log files to keep. |
| `audit.includeReadOnly` | `false` | When true, records read-only GET requests such as status, metrics, events, and audit queries. |
| `log.format` | `json` | Process log format. Supported values: `json`, `text`. |
| `log.level` | `info` | Reserved log level setting. Supported values: `debug`, `info`, `warn`, `error`. |

## Control Plane Endpoints

| Path | Purpose |
| --- | --- |
| `/healthz` | Liveness probe. Does not require Docker readiness or bearer auth. |
| `/readyz` | Readiness probe. Does not require bearer auth. |
| `/status` | Runtime API status, local node information, Scheduler Lite resource snapshot, loaded Runtime resources, listeners, and build information. |
| `/version` | Binary and Runtime contract version information. |
| `/metrics` | Prometheus-compatible runtime metrics when enabled. |
| `/audit` | Audit event query endpoint when audit logging is enabled. Requires `admin` when RBAC is enabled. |
| `/apis/aifar.io/v1/namespaces/{namespace}/runtimes/{name}` | Rendered Runtime resource API. |

When `security.bearerToken`, `security.bearerTokenFile`, or `security.rbac.enabled` is configured, all endpoints except `/healthz` and `/readyz` require `Authorization: Bearer <token>`.

RBAC roles:

| Role | Access |
| --- | --- |
| `admin` | All Runtime API operations. |
| `operator` | Read, validate, apply, and reconcile. Delete is forbidden. |
| `viewer` | Read-only GET requests. |

`/audit` is admin-only under RBAC because it can expose operator names, source IPs, request IDs, and failure reasons.

Audit events are written as JSON Lines and can also be queried through the CLI:

```bash
aifar-runtime audit --addr 127.0.0.1:18081 --token "$AIFAR_RUNTIME_TOKEN" --tail 100
aifar-runtime audit --namespace prod --name demo --operation apply --result succeeded
```

## Runtime Resource Additions

Rendered `Runtime` resources support local secrets, image pull credentials, rolling update strategy, and stricter resources:

```yaml
spec:
  nodeName: local
  nodeSelector:
    zone: edge
  secrets:
    - name: regcred
      type: registry-auth
      stringData:
        server: registry.local
        username: robot
        password: ${rendered-password}
    - name: app-env
      type: opaque
      stringData:
        API_KEY: ${rendered-api-key}
  deployments:
    - name: api
      image: registry.local/demo-api:1.0.0
      imagePullSecrets:
        - name: regcred
      strategy:
        type: RollingUpdate
        rollingUpdate:
          maxSurge: 1
          maxUnavailable: 0
      envFrom:
        - type: secret
          name: app-env
      resources:
        cpus: "0.5"
        memory: 256Mi
        memorySwap: 512Mi
        pidsLimit: 128
```

`secret.data` values are base64 encoded, while `secret.stringData` values are plain strings and override `data` keys. `registry-auth` uses `docker login --password-stdin` followed by `docker pull`. `dockerconfigjson` secrets can provide `stringData.configPath` to use an existing Docker config directory.

`spec.nodeName` is optional. When set, it must equal `node.name` on the local runtime process. `spec.nodeSelector` keys must match `node.labels`. These fields are single-node checks today and reserve the contract needed for a future etcd-backed scheduler.

Scheduler Lite runs before Docker/network/proxy actions. It rejects:

- A `Service.listenPort` already claimed by another Runtime service or ingress listener.
- An ingress route whose `listenPort + host + path` overlaps an existing Runtime ingress route. Host `*` overlaps any host.
- A Runtime whose requested `resources.cpus`, `resources.memory`, or `resources.pidsLimit` would exceed `node.allocatable` or `node.capacity`.

Resource requests are multiplied by deployment replicas. CPU is exposed in millicores under `/status.scheduler`, memory is exposed as bytes, and empty node capacity fields mean "do not enforce this resource".

Runtime status phases are:

| Phase | Meaning |
| --- | --- |
| `Pending` | Runtime accepted but reconciliation has not started provider work. |
| `Pulling` | One or more deployment images are being pulled. |
| `Starting` | Containers are being created or waited on. |
| `Running` | Desired replicas and proxy endpoints are ready. |
| `Degraded` | The Runtime is present but one or more desired replicas or endpoints are not ready. |
| `Updating` | Existing Runtime is being replaced, rolled, or self-healed. |
| `Failed` | Reconciliation failed and needs operator attention. |
| `Terminating` | Delete has started and owned resources are being removed. |

Core conditions include `SpecAccepted`, `NodeAssigned`, `ImagePulled`, `ContainerReady`, `ServicesReady`, and `IngressReady`. Deployment status includes `restarts`, which is incremented by runtime self-heal attempts.

When a rolling update fails while a previous Runtime exists, AIFAR Runtime restores the previous Runtime spec/status, removes containers from the failed new generation, and keeps existing ready containers online. The event stream records `RollbackStarted`, then `RollbackComplete` or `RollbackFailed`.

## Backup And Restore

```bash
aifar-runtime backup --config /etc/aifar-runtime/config.yaml --out /var/backups/aifar-runtime/state.json
aifar-runtime restore --config /etc/aifar-runtime/config.yaml --in /var/backups/aifar-runtime/state.json
```

Backup and restore currently support `state.backend=file`.

## Change Rule

When adding a parameter:

1. Add the field to `internal/runtimeagent/config.go`.
2. Add a default in `DefaultRuntimeConfig`.
3. Normalize and validate it in `NormalizeRuntimeConfig` and `ValidateRuntimeConfig`.
4. Add or update tests in `internal/runtimeagent/config_test.go`.
5. Add the field to `deploy/systemd/aifar-runtime.yaml`.
6. Document it in this file.

## Release Outputs

Release artifacts are produced by `make release` through `tools/release`:

| File | Purpose |
| --- | --- |
| `dist/aifar-runtime-{version}-{os}-{arch}.tar.gz` | Linux/Unix release archive. |
| `dist/aifar-runtime-{version}-{os}-{arch}.zip` | Windows release archive. |
| `dist/checksums.txt` | SHA256 checksum file for archives. |
| `dist/manifest.json` | Machine-readable release manifest with archive and binary hashes. |
| `dist/sbom.spdx.json` | SPDX 2.3 software bill of materials for Go modules. |

Release archives include `release/build.json` and `release/sbom.spdx.json` inside each platform package.
