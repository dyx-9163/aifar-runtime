# AIFAR Runtime Agent Guide

This repository contains only AIFAR Runtime.

## Rules

- Keep the CLI thin; runtime behavior belongs in `internal/runtimeagent`.
- Runtime reads rendered `Runtime` resources only. It does not read package templates directly.
- Runtime does not register services into Nacos, Eureka, Consul, or other registries.
- Tests must use fake runners/providers and must not connect to real Docker or registries.

## Validate

```powershell
go test ./...
```
