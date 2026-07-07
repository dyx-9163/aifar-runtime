# AIFAR Runtime Configuration

Runtime configuration is loaded from YAML with `aifar-runtime serve --config /etc/aifar-runtime/config.yaml`.
CLI flags such as `--listen` and `--state-dir` are only operational overrides; new runtime parameters should be added to this file format first.

## Fields

| Field | Default | Purpose |
| --- | --- | --- |
| `api.listen` | `127.0.0.1:18081` | Runtime HTTP API listen address. |
| `api.readHeaderTimeout` | `10s` | Header read timeout for the Runtime API. |
| `api.shutdownTimeout` | `30s` | Maximum graceful shutdown time for the API and proxy listeners. |
| `state.dir` | `/var/lib/aifar-runtime` | Persistent specs, statuses, events, and proxy route state. |
| `docker.command` | `docker` | Container CLI used by the runtime. |
| `docker.restartPolicy` | `unless-stopped` | Restart policy applied to managed containers. |
| `docker.addHost` | `host.docker.internal:host-gateway` | Host mapping injected into managed containers. |
| `docker.eventDebounce` | `2s` | Debounce window after Docker events before resync. |
| `docker.eventBackoff` | `5s` | Backoff after Docker event watcher failures. |
| `container.readyTimeout` | `5m` | Maximum time to wait for a container to become ready. |
| `container.readyPollInterval` | `3s` | Poll interval while waiting for container readiness. |
| `container.diagnosticsLogTail` | `120` | Number of container log lines captured on readiness failure. |
| `container.httpHealthCheckTemplate` | `wget -qO- http://127.0.0.1:%d%s >/dev/null` | Docker health check command template. It must include `%d` for port and `%s` for path. |
| `proxy.readHeaderTimeout` | `10s` | Header read timeout for Service and Ingress proxy listeners. |
| `reconcile.interval` | `30s` | Periodic full reconciliation interval. |
| `health.dockerTimeout` | `5s` | Docker readiness check timeout. |

## Change Rule

When adding a parameter:

1. Add the field to `internal/runtimeagent/config.go`.
2. Add a default in `DefaultRuntimeConfig`.
3. Normalize and validate it in `NormalizeRuntimeConfig` and `ValidateRuntimeConfig`.
4. Add or update tests in `internal/runtimeagent/config_test.go`.
5. Add the field to `deploy/systemd/aifar-runtime.yaml`.
6. Document it in this file.
