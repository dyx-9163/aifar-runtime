---
name: aifar-runtime
description: Work on the AIFAR Runtime Go repository. Use when changing, reviewing, testing, or documenting this project, including Runtime resource parsing and validation, runtimeagent reconciliation, local Docker orchestration, service and ingress proxy behavior, CLI/API handlers, state store behavior, and project design docs.
---

# AIFAR Runtime

## Product Direction

- Build AIFAR Runtime as a private-deployment, single-node Kubernetes replacement for small and edge environments.
- Keep the operator experience close to Kubernetes where it helps: rendered desired-state resources, reconciliation, stable service/ingress entrypoints, status, events, probes, metrics, and systemd-based lifecycle.
- Prefer production-operable defaults over demos. Every new runtime knob should flow through `internal/runtimeagent/config.go`, `deploy/systemd/aifar-runtime.yaml`, and `docs/config-reference.md`.

## Project Shape

- Keep the CLI in `cmd/aifar-runtime` thin. Put runtime behavior, validation, state, reconciliation, proxy routing, and Docker command construction in `internal/runtimeagent`.
- Treat rendered `Runtime` resources as the only runtime input. Do not make runtime code read package/chart templates directly.
- Do not add service registry integration. Nacos, Eureka, Consul, and similar registries are application-owned concerns.
- Preserve the v0.1 contract in `docs/aifar-runtime-design.md` and the examples in `README.md` when changing resource shape.

## Implementation Rules

- Normalize and validate a `Runtime` before provider actions.
- Keep `Service.listenPort` and `Ingress.listenPort` stable external contracts.
- Keep delete behavior narrow: remove runtime state, listeners, and owned containers only; do not delete images, data directories, external services, or registry records.
- Use `RuntimeProvider`-style seams through `CommandRunner` or fakes for tests. Do not call real Docker or real registries from tests.
- Keep HTTP handlers and CLI commands as adapters around `runtimeagent` behavior.
- Preserve optional API security and observability as first-class control-plane features for private deployments.
- Keep `state.backend` abstracted. `file` is the only implemented backend today; `etcd` is reserved for the future clustered control plane and must not silently fall back to local file state.
- Treat Runtime `secrets` as rendered private-deployment material. Do not log registry passwords or secret values except where Docker requires env injection.
- Keep Runtime phases and conditions useful for operators. Prefer explicit `Pending`, `Pulling`, `Starting`, `Running`, `Degraded`, `Updating`, `Failed`, and `Terminating` states over vague success/failure strings.
- Keep self-healing bounded and observable. Automatic restarts must honor configured limits/backoff and write status/events.
- Keep the single-node `node` model compatible with future etcd scheduling: validate `nodeName` and `nodeSelector`, but do not introduce distributed behavior without an explicit state backend implementation.

## GitHub Publishing

- After code or documentation changes are complete and validation passes, commit and push the intended project changes to `origin` by default.
- Use the current branch unless the user asks for a PR branch. Direct push to `main` is acceptable for this private project when GitHub allows it.
- Do not stage unrelated user changes silently. If the worktree is mixed or ownership is unclear, ask before committing.
- If GitHub authentication, network access, or branch protection blocks the push, report the blocker clearly and keep the local commit intact.

## Testing

Run the full test suite after code changes:

```powershell
go test ./...
```

When adding behavior, prefer focused tests with fake runners/providers:

- Resource contract and compatibility: `internal/runtimeagent/spec_test.go`.
- Reconciliation, state, proxy, endpoint, and Docker command behavior: `internal/runtimeagent/ingress_test.go`.
- CLI/API adapter behavior: `cmd/aifar-runtime/main_test.go`.

## Review Checklist

- Does the change keep runtime input rendered-only?
- Does it avoid registry ownership?
- Are CLI changes backed by runtimeagent behavior instead of duplicating logic?
- Are Docker interactions covered through fakes?
- Are status, event, and state-store effects deterministic enough for tests?
