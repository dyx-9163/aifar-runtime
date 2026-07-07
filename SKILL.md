---
name: aifar-runtime
description: Work on the AIFAR Runtime Go repository. Use when changing, reviewing, testing, or documenting this project, including Runtime resource parsing and validation, runtimeagent reconciliation, local Docker orchestration, service and ingress proxy behavior, CLI/API handlers, state store behavior, and project design docs.
---

# AIFAR Runtime

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
