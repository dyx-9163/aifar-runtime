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
curl -fsS -H "Authorization: Bearer $(sudo cat /etc/aifar-runtime/secrets/api-token)" http://127.0.0.1:18081/metrics
```

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
| State looks stale | Confirm `state.dir` ownership and restart the service to trigger load plus resync. |
| Private image pull fails | Confirm the referenced `imagePullSecrets` secret and registry credentials, then inspect Docker auth errors in the event stream/logs. |

## Release Package

```bash
make release VERSION=0.1.0
ls dist/aifar-runtime-0.1.0-linux-amd64.tar.gz
```

The release package contains the Linux binary, systemd deployment files, README, project skill guide, and docs.
