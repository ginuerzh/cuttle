# Docker

`llm-gw` depends on published module tags, so the Docker build context is this
directory. Build with:

```bash
docker build -t llm-gw .
```

The build stage downloads the `core`, `x` and `llm-api-converter` versions
recorded in `go.mod`. Use the `GOPROXY` and `GOSUMDB` build args for restricted
networks.

Pass the build identity explicitly when building outside CI:

```bash
docker build \
  --build-arg VERSION=v0.1.0 \
  --build-arg COMMIT="$(git rev-parse HEAD)" \
  --build-arg BUILT_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -t llm-gw .
```

`VERSION` defaults to `dev`. `llm-gw version`, `/api/health`, and the admin
sidebar show the injected values.

## Zero-config first run

The image starts with no config file:

```bash
docker run -d --name llm-gw \
  -p 8787:8787 -p 8788:8788 \
  -v llm-gw-data:/data \
  ghcr.io/ginuerzh/llm-gw:latest

docker logs llm-gw
```

The first start creates `/data/llm-gw.yaml` and `/data/stats.sqlite`. The log
contains a one-time admin password. Open `http://localhost:8788/`, log in, and
create the provider, node, route and entrypoint in the console.

The image sets `LLM_GW_DATA=/data` (the data directory on every start) and
`LLM_GW_ADMIN_LISTEN=:8788` (a first-run default). The binary default is
`127.0.0.1:8788`; the image changes it so the published admin port works
without extra configuration.

To choose the password before the first start:

```bash
docker run -d --name llm-gw \
  -p 8787:8787 -p 8788:8788 \
  -e LLM_GW_ADMIN_PASSWORD='choose-a-long-password' \
  -v llm-gw-data:/data \
  ghcr.io/ginuerzh/llm-gw:latest
```

The three config-value variables are first-run defaults. Once a config exists,
the file and console are authoritative and those variables are ignored (a
differing value logs a warning). `LLM_GW_DATA` is the data directory used on
every start. To keep the admin API inside the container network on a fresh
start, set `LLM_GW_ADMIN_LISTEN=127.0.0.1:8788`.

`LLM_GW_ADMIN_PASSWORD` is visible in the container environment. Use an env file
or an orchestrator secret to inject it. The generated config stores only the
bcrypt hash.

## Existing config mount

```bash
docker run -d --name llm-gw \
  -p 8787:8787 -p 8788:8788 \
  -v "$PWD/llm-gw.yaml:/etc/llm-gw/llm-gw.yaml:ro" \
  -v llm-gw-data:/data \
  ghcr.io/ginuerzh/llm-gw:latest
```

`/etc/llm-gw/llm-gw.yaml` is found when `/data/llm-gw.yaml` does not exist. Use
`serve -c /etc/llm-gw/llm-gw.yaml` as the container command to be explicit.
Admin edits write the config file, so use a writable mount if you want to manage
the gateway from the console; with a read-only mount, edit the host file and send
`SIGHUP`.

`/data` is the working directory. A relative `stats.path` in a hand-written
config persists there; generated configs use an absolute path. The image runs as
UID/GID `10001`; mounted configs must be readable by that user and a
bind-mounted `/data` must be writable by it. A newly created `/data` is `0700`;
a pre-existing or bind-mounted directory keeps its mode, and startup warns if it
is accessible by group or other.

## GitHub Actions

`.github/workflows/docker.yml` builds `linux/amd64` and `linux/arm64` images and
pushes them to GHCR on pushes to `main` and `v*` tags.

The workflow builds the image from the `llm-gw` context. Dependency versions are
the tags in `go.mod`; update them with `go get` and `go mod tidy`.

The workflow derives `VERSION` from the git ref: a `vX.Y.Z` tag uses the tag
name, and a branch build uses `<branch>-<short-sha>`. `COMMIT` and `BUILT_AT`
are passed alongside it, so `llm-gw version` and `/api/health` identify the
exact build.
