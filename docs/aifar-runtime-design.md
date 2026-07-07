# AIFAR Runtime Resource Contract v0.1 / AIFAR Runtime 资源契约 v0.1

AIFAR Runtime 是单机、类 Kubernetes 的运行时控制器。它只负责运行时调和、稳定宿主机入口和状态观测；服务发现由应用自注册。

AIFAR Runtime is a single-node Kubernetes-like runtime controller. It owns reconciliation, stable host entrypoints, and observed status only; service discovery is application-owned.

## 目标 / Goals

- 输入只接收镜像和期望状态。
  Input is images plus desired state only.
- 核心资源只有 `Deployment`、`Service`、`Ingress`。
  Core resources are `Deployment`, `Service`, and `Ingress`.
- 容器 IP 是内部细节；外部访问使用宿主机稳定端口。
  Container IP is internal; external access uses stable host ports.
- Runtime 不做 Nacos/Eureka/Consul 注册。
  Runtime does not register into Nacos/Eureka/Consul.
- Package/Chart 可模拟 Helm，但必须先渲染为 `rendered-runtime.yaml`。
  Package/Chart may simulate Helm, but it must render `rendered-runtime.yaml` first.

## 分层 / Layering

```text
aifar-package + values
  -> rendered-runtime.yaml
  -> aifar-runtime apply
  -> Docker + Service Proxy + Ingress
```

应用如果需要注册中心，自己注册：

Applications self-register when they need a registry:

```text
Application -> Nacos/Eureka/Consul
```

注册地址应是 Runtime 宿主机 IP + `Service.listenPort`，不是容器 IP。

The registered endpoint should be Runtime host IP plus `Service.listenPort`, not container IP.

## Package 结构 / Package Structure

源包：

Source package:

```text
aifar-package/
  Chart.yaml
  values.yaml
  values.schema.json
  images.lock
  templates/
    runtime.yaml.tpl
  examples/
    values-dev.yaml
    values-prod.yaml
  README.md
```

渲染输出：

Rendered output:

```text
dist/
  prod/
    rendered-runtime.yaml
    render-values.yaml
    render-manifest.json
```

约束：

Rules:

- `rendered-runtime.yaml` 是 Runtime 唯一输入。
  `rendered-runtime.yaml` is the only Runtime input.
- 不包含模板语法、`status`、`registryProjections`。
  It contains no template syntax, `status`, or `registryProjections`.
- 可包含应用自注册需要的 env，如 `SERVICE_REGISTER_IP`、`SERVICE_REGISTER_PORT`。
  It may include env needed by application self-registration, such as `SERVICE_REGISTER_IP` and `SERVICE_REGISTER_PORT`.

## Runtime YAML 最小形态 / Minimal Runtime YAML

```yaml
apiVersion: aifar.io/v1
kind: Runtime
metadata:
  name: demo
  namespace: prod
spec:
  network: aifar-runtime
  deployments:
    - name: api
      image: registry.local/demo-api:1.0.0
      replicas: 2
      ports:
        - name: http
          containerPort: 9000
      env:
        SERVICE_REGISTER_IP: 192.168.74.132
        SERVICE_REGISTER_PORT: "19000"
      healthCheck:
        httpGet:
          path: /health
          port: http
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

## 资源字段 / Resource Fields

### Runtime

| Field | Required | Notes |
| --- | --- | --- |
| `apiVersion` | no | default `aifar.io/v1` |
| `kind` | no | default `Runtime` |
| `metadata.name` | yes | lowercase letters, digits, `-` |
| `metadata.namespace` | no | default `default` |
| `spec.nodeName` | no | optional single-node assignment; must match local `node.name` when set |
| `spec.nodeSelector` | no | optional label selector against local `node.labels`; reserved for future scheduler |
| `spec.network` | no | default `aifar-runtime` |
| `spec.deployments` | yes | workload list |
| `spec.services` | yes | stable service list |
| `spec.ingress` | no | external entry rules |

### DeploymentSpec

| Field | Required | Notes |
| --- | --- | --- |
| `name` | yes | stable workload name |
| `image` | yes | OCI image reference |
| `replicas` | no | default `1`, allows `0` |
| `selector` | no | default `app=<name>` |
| `ports` | no | named container ports |
| `env` | no | non-sensitive env |
| `envFrom` | no | env file or secret reference |
| `volumes` | no | host path or named volume |
| `resources` | no | CPU/memory limits |
| `healthCheck` | no | readiness check |

### ServiceSpec

| Field | Required | Notes |
| --- | --- | --- |
| `name` | yes | stable service name |
| `selector` | yes | selects deployments/pods |
| `port` | yes | service port |
| `targetPort` | yes | container port name or number |
| `listenPort` | no | stable host proxy port |
| `protocol` | no | default `http`; may support `tcp` |
| `affinityPolicy` | no | `none`, `client-ip`, `header` |

### IngressSpec

| Field | Required | Notes |
| --- | --- | --- |
| `name` | yes | ingress name |
| `provider` | no | default `builtin` |
| `host` | no | default `*` |
| `listenPort` | yes | host ingress port |
| `tls` | no | certificate reference and policy |
| `routes` | yes | path to service mappings |

## 应用自注册 / Application Self-Registration

Runtime 不保存注册中心凭据，不调用注册中心 API，不替应用执行 register/deregister/heartbeat。

Runtime does not store registry credentials, call registry APIs, or perform register/deregister/heartbeat for applications.

应用自注册规则：

Application rules:

- 注册 IP 使用 Runtime 宿主机 IP。
  Register Runtime host IP.
- 注册端口使用对应 `Service.listenPort`。
  Register the matching `Service.listenPort`.
- 不注册容器 IP 或容器内部端口。
  Do not register container IP or container-internal ports.
- 注册中心地址、命名空间、分组、账号和密钥属于应用配置。
  Registry address, namespace, group, account, and secrets are application config.

## Status / 状态

Runtime 必须区分 `spec` 和 `status`。

Runtime must separate `spec` and `status`.

`status` 至少包含：

`status` includes at least:

- `observedGeneration`
- `phase`: `Pending`, `Pulling`, `Starting`, `Running`, `Degraded`, `Updating`, `Failed`, `Terminating`
- `conditions`
- `deployments`
- `services`
- `ingress`

核心 conditions：

Core conditions:

- `SpecAccepted`
- `NodeAssigned`
- `ImagePulled`
- `ContainerReady`
- `ServicesReady`
- `IngressReady`

## 调和规则 / Reconcile Rules

1. 先 normalize 和 validate，再执行任何 provider 动作。
   Normalize and validate before provider actions.
2. 同一 spec 重复 apply 必须幂等。
   Reapplying the same spec must be idempotent.
3. Scheduler Lite must admit a Runtime before provider actions. It checks node assignment, global listener conflicts, ingress `listenPort + host + path` conflicts, and node resource capacity.
4. `Service.listenPort` 和 `Ingress.listenPort` 是外部契约，不应漂移。
   `Service.listenPort` and `Ingress.listenPort` are external contracts and must not drift.
5. `replicas: 0` 表示下线 deployment。
   `replicas: 0` means taking a deployment offline.
6. 删除只删除 Runtime state、listeners、owned containers，不删除数据目录、镜像、外部服务或注册中心记录。
   Deletion removes only Runtime state, listeners, and owned containers; it does not delete data directories, images, external services, or registry records.

## State Store / 状态存储

v0.1 使用本机 JSON 文件：

v0.1 uses local JSON files:

```text
/var/lib/aifar-runtime/
  specs/<namespace>/<name>.json
  status/<namespace>/<name>.json
  events/<namespace>/<name>.jsonl
  locks/<namespace>/<name>.lock
  proxy/
    services.json
    ingress.json
```

Docker labels：

```text
aifar.runtime/managed=true
aifar.runtime/namespace=<namespace>
aifar.runtime/name=<runtime-name>
aifar.runtime/deployment=<deployment-name>
aifar.runtime/replica=<replica-index>
aifar.runtime/generation=<generation>
```

## API / CLI 草案

HTTP API:

| Method | Path |
| --- | --- |
| `GET` | `/healthz` |
| `GET` | `/readyz` |
| `POST` | `/apis/aifar.io/v1/namespaces/{namespace}/runtimes/{name}:validate` |
| `PUT` | `/apis/aifar.io/v1/namespaces/{namespace}/runtimes/{name}` |
| `GET` | `/apis/aifar.io/v1/namespaces/{namespace}/runtimes/{name}` |
| `GET` | `/apis/aifar.io/v1/namespaces/{namespace}/runtimes/{name}/status` |
| `GET` | `/apis/aifar.io/v1/namespaces/{namespace}/runtimes/{name}/events` |
| `DELETE` | `/apis/aifar.io/v1/namespaces/{namespace}/runtimes/{name}` |

CLI:

```powershell
aifar-runtime serve --listen 127.0.0.1:18080 --state-dir /var/lib/aifar-runtime
aifar-runtime validate -f rendered-runtime.yaml
aifar-runtime apply -f rendered-runtime.yaml
aifar-runtime status --namespace prod --name demo
aifar-runtime events --namespace prod --name demo --tail 100
aifar-runtime delete --namespace prod --name demo
```

## 开发里程碑 / Milestones

### M0 Contract

- 建立 `Runtime`、`Metadata`、`RuntimeSpec`、`RuntimeStatus`、`Condition`。
- 移除核心模型里的 `NacosSpec`、`RegistryProjection`。
- 补 normalize / validate 单元测试。

### M1 State + API

- 实现 JSON state store 和 events JSONL。
- 实现 health、ready、validate、apply、status、events、delete。
- CLI 调用通用 API。

### M2 Controller

- controller 只依赖 `RuntimeProvider`、`ServiceProxy`、`IngressProvider`。
- 使用 fake provider 覆盖成功、失败、幂等、删除边界。

### M3 Docker + Proxy

- Docker provider 实现 network、deployment、endpoint、diagnostics。
- Service proxy 与 builtin ingress 复用 listener 管理。
- 宿主机端口在更新期间保持稳定。

### M4 Package Support

- Runtime status 输出 Service host/listenPort。
- Package/Chart 根据 Service listener 生成应用自注册配置。
- Runtime 不验证注册中心状态。

### M5 Acceptance

- 跑通 validate/apply/status/events/delete 闭环。
- 跑通 nginx 或本地测试镜像 smoke。
- 企业级检查通过后再进入 UI 或 Artifact Import Layer。
