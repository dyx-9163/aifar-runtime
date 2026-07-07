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

## Daily Checks

```bash
systemctl status aifar-runtime
journalctl -u aifar-runtime -n 200 --no-pager
aifar-runtime health --config /etc/aifar-runtime/config.yaml
aifar-runtime status --addr 127.0.0.1:18081
```

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
