# Chainguard static base image, with a release per base update

The container image is built on `cgr.dev/chainguard/static`, pinned by digest. Renovate bumps that digest weekly as a `fix(deps)` commit and automerges it once `build` passes, so every base update ships as a new patch release through release-please. We never rebuild or re-push an existing version tag once its GitHub release is published. While the release is still a draft, a failed release workflow may be re-run, even though the re-run pushes a new image digest under the same tag.

## Considered Options

- **distroless/base**: carries glibc and openssl, which the statically linked binary never uses, and has a noisier CVE report.
- **wolfi-base**: includes a shell and `apk`, so it is larger than what it would replace.
- **Docker Hardened Images (`dhi.io/static`)**: richer attestations (VEX, SLSA L3), but every pull needs a Docker login. That would mean a secret in CI and in Renovate, and a login for anyone building the Dockerfile.
- **Rebuilding existing tags in place on a schedule**: invisible to consumers who pin a version tag, orphans the cosign signatures and provenance attached to the old digest, and breaks immutable version tags. This is worth revisiting if consumers move to auto-updaters that follow digests, such as Flux with `digestReflectionPolicy: Always`.

## Consequences

Releases with no code changes are expected: their only changelog entry is the base image bump. Renovate uses `chore(deps)` for Dockerfile updates by default, which would never trigger a release, so the `fix` override in `renovate.json` is load-bearing.

A release that fails after the image is pushed, for example while signing, leaves the version tag on an image whose release was never published. Re-running it rebuilds the image, and the build is not reproducible, so the tag moves to a new digest. This is accepted: the GitHub release stays a draft until GoReleaser finishes, so no published release ever pointed at the old digest. Only someone pulling the tag, or `latest`, in the minutes between the push and the re-run sees it change. The old digest is left untagged; any signatures already made for it stay in the transparency log. Making the image reproducible instead was considered and rejected: it meant dropping buildx's provenance attestation, and still held only while the Go version stayed the same, all for a rare failure.
