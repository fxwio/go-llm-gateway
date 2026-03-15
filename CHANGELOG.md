# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]
- Add hardened admin/debug endpoint protection with CIDR + bearer token enforcement.
- Add build metadata endpoint and release-aware build pipeline.
- Add Helm chart, deployment runbooks, and test-environment publish command.

## [0.6.0] - 2026-03-15
### Added
- Standardized release process with semantic versioning, changelog generation, and image tagging.
- Production-ready Helm chart with readiness/liveness probes, HPA, ConfigMap, Secret, and pod hardening.
- CI quality gates for lint, static analysis, security scanning, benchmarks, and integration tests.
- Protected `/admin/*` and `/debug/pprof/*` surfaces with dedicated admin policy.

### Changed
- Runtime image is now distroless and non-root by default.
- Build metadata is injected through ldflags and exposed via `/version`.
