# AIFAR Runtime

AIFAR Runtime 是一个单机、类 Kubernetes 的运行时控制器。它接收镜像和期望状态，把应用调和到本机 Docker，并暴露稳定的宿主机 Service / Ingress 入口。

AIFAR Runtime is a single-node Kubernetes-like runtime controller. It accepts images plus desired state, reconciles applications onto local Docker, and exposes stable host-level Service / Ingress entrypoints.

## Scope / 范围

- Core resources: `Deployment`, `Service`, `Ingress`.
- Runtime input: one rendered `Runtime` resource, usually `rendered-runtime.yaml`.
- Runtime provider: local Docker.
- Status model: `Pending`, `Pulling`, `Starting`, `Running`, `Degraded`, `Updating`, `Failed`, `Terminating`.
- Self-heal: exited or unhealthy managed containers can be replaced automatically during reconciliation.
- Node model: single-node identity and selectors are available now, with a contract reserved for future clustered scheduling.
- Scheduler Lite: Runtime apply is admitted before Docker actions, including global host-port conflict checks and node resource capacity checks.
- Rollback: failed rolling updates keep the previous Runtime spec/status and remove failed new-generation containers.
- Audit log: mutating API calls and local backup/restore operations are written as JSONL audit events with actor, role, request ID, target, result, and duration.
- Service discovery: application-owned. Runtime does not register into Nacos, Eureka, Consul, or other registries.
- Delete behavior: removes Runtime state, listeners, and owned containers only. It does not remove images, data directories, external services, or registry records.

## Layout / 目录

```text
cmd/aifar-runtime/          CLI and local HTTP API
internal/runtimeagent/      runtime contract, reconciler, proxy, state store
deploy/systemd/             systemd unit, config, sysusers, tmpfiles examples
runtimes/                   example rendered Runtime resources for testing
docs/config-reference.md    runtime configuration reference
docs/operations-runbook.md  install, upgrade, rollback, troubleshooting
docs/aifar-runtime-design.md resource contract design
```

## Build / 构建

```powershell
go test ./...
go vet ./...
go build -trimpath -o bin/aifar-runtime.exe ./cmd/aifar-runtime
```

On Linux hosts with `make`:

```bash
make check
make build
make release VERSION=0.1.0
make security VERSION=0.1.0
```

`make release` writes versioned archives, `dist/checksums.txt`, `dist/manifest.json`, and `dist/sbom.spdx.json`.

## CLI / 命令

```powershell
aifar-runtime serve --listen 127.0.0.1:18081 --state-dir /var/lib/aifar-runtime
aifar-runtime backup --out backup.json
aifar-runtime restore --in backup.json
aifar-runtime validate -f rendered-runtime.yaml
aifar-runtime validate -f runtimes/demo-nginx.yaml
aifar-runtime apply -f rendered-runtime.yaml
aifar-runtime status --namespace prod --name demo
aifar-runtime events --namespace prod --name demo --tail 100
aifar-runtime audit --tail 100 --namespace prod --name demo
aifar-runtime delete --namespace prod --name demo
```

## systemd / Linux 服务

示例 unit 和环境文件位于 `deploy/systemd/`。

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
sudo systemctl status aifar-runtime
```

默认服务依赖本机 Docker，并以专用用户 `aifar-runtime` 运行。运行参数集中在 `/etc/aifar-runtime/config.yaml`。该用户需要通过 `docker` supplementary group 访问 Docker socket；如果发行版的 Docker 权限策略不同，需要调整 `deploy/systemd/aifar-runtime.service`。

生产环境建议配置 `security.bearerTokenFile`，必要时同时配置 `security.tlsCertFile` / `security.tlsKeyFile`。控制面提供 `/healthz`、`/readyz`、`/status`、`/version`、`/metrics`、`/audit` 和 Runtime API。

更多配置和运维步骤见 `docs/config-reference.md` 与 `docs/operations-runbook.md`。

## Minimal Runtime YAML / 最小 YAML

```yaml
apiVersion: aifar.io/v1
kind: Runtime
metadata:
  name: demo
  namespace: prod
spec:
  nodeName: local
  network: aifar-runtime
  secrets:
    - name: regcred
      type: registry-auth
      stringData:
        server: registry.local
        username: robot
        password: rendered-password
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
      replicas: 2
      resources:
        cpus: "0.5"
        memory: 256Mi
      ports:
        - name: http
          containerPort: 9000
      env:
        SERVICE_REGISTER_IP: 192.168.74.132
        SERVICE_REGISTER_PORT: "19000"
  services:
    - name: api
      selector:
        app: api
      port: 9000
      targetPort: http
      listenPort: 19000
  ingress:
    - name: public
      provider: builtin
      listenPort: 8080
      routes:
        - path: /api
          serviceName: api
          servicePort: 9000
```
