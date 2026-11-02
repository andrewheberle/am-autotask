# Go toolchain pinned in go.mod, with a release per update

Builds, tests and releases use the Go toolchain named by the `toolchain` line in `go.mod`, read by `actions/setup-go` through `go-version-file: go.mod`, rather than whatever `stable` is when a workflow runs. The binary is statically linked, so the Go standard library ships inside it and is as much a part of the image's base as `cgr.dev/chainguard/static` (ADR-0009). It is treated the same way: Renovate updates the `toolchain` line as a `fix(deps)` commit, so every toolchain update ships as a release, and patch updates, which carry Go's security fixes, automerge once `build` passes. Minor updates are merged by hand, as they can bring new vet and lint findings. The `go` line stays the minimum supported version and is changed only on purpose; lint still runs against `oldstable` and `stable` to check that range.

## Considered Options

- **`go-version: stable` in every workflow**: no updates to merge, and with weekly base image releases a Go fix reaches a release within about a week. But the compiler changes without any commit or changelog entry, a Go release landing between pull request CI and the release run ships a binary built by a compiler CI never tested, and rebuilding a tag later uses a different compiler.
- **Pinning the version in each workflow's `go-version`**: the same effect, but three copies to keep in step, and local builds would not follow them. The `toolchain` line is one copy, which `go` itself honours when building locally.

## Consequences

About one toolchain update a month, plus a minor update every six months. The `fix` override in `renovate.json` is load-bearing: by default a toolchain update would be a `chore(deps)` commit, which never triggers a release, and the pinned toolchain would fall behind Go's security fixes, which is worse than `stable`.

golangci-lint refuses to lint for a Go version newer than the one its own binary was built with, and it takes that version from the `toolchain` line. So a minor toolchain update fails lint until golangci-lint releases a binary built with that minor version, which is one more reason minor updates wait for a person to merge them.
