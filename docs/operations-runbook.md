# AIFAR Runtime Operations Runbook

## Install

```bash
go build -trimpath -o aifar-runtime ./cmd/aifar-runtime
sudo install -m 0755 aifar-runtime /usr/local/bin/aifar-runtime
sudo install -m 0644 deploy/systemd/aifar-runtime.sysusers.conf /etc/sysusers.d/aifar-runtime.conf
sudo install -m 0644 deploy/systemd/aifar-runtime.tmpfiles.conf /etc/tmpfiles.d/aifar-runtime.conf
sudo systemd-sysusers /etc/sysusers.d/aifar-runtime.conf
sudo systemd-tmpfiles --create /etc/tmpfiles.d/aifar-runtime.conf
sudo install -m 0644 deploy/systemd/aifar-runtime.yaml /etc/aifar-runtime/config.yaml
sudo install -m 0644 deploy/systemd/aifar-runtime.env /etc/aifar-runtime/aifar-runtime.env
sudo install -m 0644 deploy/systemd/aifar-runtime.service /etc/systemd/system/aifar-runtime.service
sudo systemctl daemon-reload
sudo systemctl enable --now aifar-runtime
```

The service runs as `aifar-runtime` and needs access to Docker through the `docker` supplementary group. If the host uses a different Docker socket policy, adjust `deploy/systemd/aifar-runtime.service`.

For private deployments, put the Runtime API token in a root-owned file and point `security.bearerTokenFile` at it:

```bash
sudo install -m 0750 -o root -g aifar-runtime -d /etc/aifar-runtime/secrets
sudo sh -c 'umask 077 && printf "%s\n" "<replace-with-random-token>" > /etc/aifar-runtime/secrets/api-token'
sudo chgrp aifar-runtime /etc/aifar-runtime/secrets/api-token
```

For multi-principal access, enable `security.rbac.enabled` and configure token files with one of these roles: `admin`, `operator`, `viewer`.

## Daily Checks

```bash
systemctl status aifar-runtime
journalctl -u aifar-runtime -n 200 --no-pager
aifar-runtime health --config /etc/aifar-runtime/config.yaml
aifar-runtime status --addr 127.0.0.1:18081 --token "$(sudo cat /etc/aifar-runtime/secrets/api-token)"
aifar-runtime audit --addr 127.0.0.1:18081 --token "$(sudo cat /etc/aifar-runtime/secrets/api-token)" --tail 50
curl -fsS -H "Authorization: Bearer $(sudo cat /etc/aifar-runtime/secrets/api-token)" http://127.0.0.1:18081/metrics
```

Review `/status` for `node.name`, `scheduler.requested`, `scheduler.available`, active listeners, Runtime phases, and restart counts. Review `/audit` for recent apply/delete/backup/restore operations and denied requests. A healthy steady state should show Runtime phase `Running` and deployment `ready == replicas`.

## Backup And Restore

```bash
sudo install -m 0750 -o aifar-runtime -g aifar-runtime -d /var/backups/aifar-runtime
sudo -u aifar-runtime aifar-runtime backup --config /etc/aifar-runtime/config.yaml --out /var/backups/aifar-runtime/state-$(date -u +%Y%m%dT%H%M%SZ).json
sudo systemctl stop aifar-runtime
sudo -u aifar-runtime aifar-runtime restore --config /etc/aifar-runtime/config.yaml --in /var/backups/aifar-runtime/state.json
sudo systemctl start aifar-runtime
```

Restore writes desired state, status, and events back into the file state backend. It does not restore container images or external volumes.

## Upgrade

```bash
go build -trimpath -o aifar-runtime ./cmd/aifar-runtime
sudo install -m 0755 aifar-runtime /usr/local/bin/aifar-runtime
sudo systemctl restart aifar-runtime
systemctl status aifar-runtime
```

## Rollback

Keep the previous binary as `/usr/local/bin/aifar-runtime.previous` before upgrade:

```bash
sudo install -m 0755 /usr/local/bin/aifar-runtime.previous /usr/local/bin/aifar-runtime
sudo systemctl restart aifar-runtime
```

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Service cannot start | `journalctl -u aifar-runtime -n 200 --no-pager` and validate `/etc/aifar-runtime/config.yaml`. |
| Docker readiness fails | Confirm Docker is running and the service user can access `/var/run/docker.sock`. |
| Service or Ingress port is unavailable | Check whether another process owns the listen port with `ss -ltnp`. |
| Runtime status is `Failed` | Inspect `aifar-runtime events --namespace <ns> --name <name>` and container logs. |
| An unexpected change happened | Inspect `aifar-runtime audit --namespace <ns> --name <name> --tail 100` for actor, request ID, result, and source IP. |
| API access is denied | Check RBAC token role and inspect `aifar-runtime audit --actor <name> --result denied`. |
| Runtime status is `Degraded` | Check deployment `ready`, `restarts`, and events for `ContainerRestarting` or `RestartLimitExceeded`. |
| Runtime is rejected by node checks | Confirm `spec.nodeName` matches `node.name`, and `spec.nodeSelector` matches `node.labels` in `/etc/aifar-runtime/config.yaml`. |
| Runtime is rejected by port admission | Check whether another Runtime already owns the Service `listenPort`, or whether an Ingress route has the same `listenPort`, overlapping host, and same path. |
| Runtime is rejected by capacity admission | Check `/status.scheduler`, then adjust `node.allocatable`, reduce replicas, or lower deployment `resources`. |
| Rolling update is rolled back | Inspect events for `RollbackStarted`, `RollbackComplete`, or `RollbackFailed`, then check the failed generation container diagnostics in the event message and runtime logs. |
| A container keeps restarting | Inspect the deployment health check, container exit code, recent logs, and `selfHeal.maxRestarts` / `selfHeal.backoff`. |
| State looks stale | Confirm `state.dir` ownership and restart the service to trigger load plus resync. |
| Private image pull fails | Confirm the referenced `imagePullSecrets` secret and registry credentials, then inspect Docker auth errors in the event stream/logs. |

## Release Package

```bash
make release VERSION=0.1.0
ls dist/aifar-runtime-0.1.0-linux-amd64.tar.gz
```

The release package contains the Linux binary, systemd deployment files, README, project skill guide, and docs.
