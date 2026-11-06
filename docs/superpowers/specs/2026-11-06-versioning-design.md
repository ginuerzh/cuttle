# llm-gw versioning design

Date: 2026-11-06
Status: proposed

## Intent

Every llm-gw build must identify itself, so an update is visible without
inspecting image digests. Release builds use semver tags; `main` builds use a
unique automatic version.

## Goals

- `llm-gw version` and `llm-gw --version` print the build identity.
- The startup log, `GET /api/health`, and the left navigation panel show it.
- A `v*` tag builds `vX.Y.Z`; a branch build produces `<branch>-<short-sha>`;
  a plain local build reports `dev`.
- Commit and build time are included when available.
- CI passes the metadata; the Dockerfile accepts it as build args.

## Non-goals

- Automatic semver calculation, changelog generation, or release notes.
- Baking the version into the web bundle at build time.
- Serving version information on the data-plane port.

## Version rules

| Build | `version` | `commit` | `builtAt` |
| --- | --- | --- | --- |
| Git tag `v0.1.0` | `v0.1.0` | full SHA | UTC build time |
| `main` push | `main-<sha7>` | full SHA | UTC build time |
| Local `go build` / plain `docker build` | `dev` | empty | empty |

The first release tag is `v0.1.0`, created on `main` after this change is
merged and verified.

## Approaches considered

- **A. Explicit build-time injection (chosen).** CI computes the version and
  the Dockerfile forwards `VERSION`, `COMMIT`, `BUILT_AT` to `-ldflags -X`.
  Deterministic, works without `.git`, identical for local and CI builds.
- **B. Runtime `debug.ReadBuildInfo` + `git describe`.** Automatic commit/time,
  but the tag is not available from Go build info and `.git` may be absent in
  the Docker context.
- **C. A committed version file bumped per release.** Reviewable, but manual,
  and branch builds still cannot distinguish individual updates.

## Design

### `internal/version`

A small package with `Version`, `Commit`, `BuildDate` string variables and:

- `Current()` returning a JSON-friendly struct.
- `ShortCommit()` returning the first seven characters.
- `String()` returning `v0.1.0 (694deb5)` or `dev`, plus build time when set.

The variables are populated by
`-X github.com/go-gost/llm-gw/internal/version.<Var>`.

### CLI and startup

`run` recognizes `version`, `-v`, `--version` before `serve`, prints
`llm-gw <String()>`, and updates the usage error. The structured startup log
adds the same identity once the logger is ready.

### Admin API and console

`GET /api/health` keeps `ok` and adds flat `version`, `commit` (when set), and
`builtAt` (when set) fields. The web client calls it once and shows the version
only at the bottom of the left navigation panel, below the logout button. It is
hidden if the call fails. Tag builds show `v0.1.0 · 694deb5`; branch builds
already contain the short commit, so they show `main-694deb5` without repeating
it. The full commit and build time are available as a tooltip.

### Docker and CI

The Docker build stage gets `ARG VERSION=dev`, `ARG COMMIT=`, `ARG BUILT_AT=`
and passes them through `-ldflags`. The workflow resolves the version before
the build: tag refs use the tag name, other refs use
`<ref_name>-<sha7>`; `built_at` is the current UTC timestamp. Both values are
forwarded as build args.

### Tests and docs

- Unit tests for version formatting and the health payload.
- A Vitest for the sidebar version label helper.
- README and DOCKER.md document `llm-gw version`, the build args, and the
  release flow.

## Release flow

1. Merge to `main`; CI publishes `main`/`sha-*`/`latest`.
2. Create and push an annotated `v0.1.0` tag.
3. CI publishes `0.1.0`, `0.1`, `latest`, and the tagged image.
4. Verify `llm-gw version`, `/api/health`, and the left navigation panel.

## Deferred

- Changelog generation and release notes.
- Version display on the login screen.
- Falling back to Go VCS build info when the build args are absent.
