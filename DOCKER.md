# Docker

`cuttle` depends on published module tags, so the Docker build context is this
directory. Build with:

```bash
docker build -t cuttle .
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
  -t cuttle .
```

`VERSION` defaults to `dev`. `cuttle version`, `/api/health`, and the admin
sidebar show the injected values.

## Zero-config first run

The image starts with no config file:

```bash
docker run -d --name cuttle \
  -p 8787:8787 -p 8788:8788 \
  -v cuttle-data:/data \
  ghcr.io/ginuerzh/cuttle:latest

docker logs cuttle
```

The first start creates `/data/cuttle.yaml` and `/data/stats.sqlite`. The log
contains a one-time admin password. Open `http://localhost:8788/`, log in, and
create the provider, node, route and entrypoint in the console.

The image sets `CUTTLE_DATA=/data` (the data directory on every start; the
binary default is `<os.UserConfigDir>/cuttle`, so the env var is what keeps the
container on `/data`) and `CUTTLE_ADMIN_LISTEN=:8788` (a first-run default). The
binary default is `127.0.0.1:8788`; the image changes it so the published admin
port works without extra configuration.

To choose the password before the first start:

```bash
docker run -d --name cuttle \
  -p 8787:8787 -p 8788:8788 \
  -e CUTTLE_ADMIN_PASSWORD='choose-a-long-password' \
  -v cuttle-data:/data \
  ghcr.io/ginuerzh/cuttle:latest
```

The three config-value variables are first-run defaults. Once a config exists,
the file and console are authoritative and those variables are ignored (a
differing value logs a warning). `CUTTLE_DATA` is the data directory used on
every start. To keep the admin API inside the container network on a fresh
start, set `CUTTLE_ADMIN_LISTEN=127.0.0.1:8788`.

`CUTTLE_ADMIN_PASSWORD` is visible in the container environment. Use an env file
or an orchestrator secret to inject it. The generated config stores only the
bcrypt hash.

## Existing config mount

```bash
docker run -d --name cuttle \
  -p 8787:8787 -p 8788:8788 \
  -v "$PWD/cuttle.yaml:/etc/cuttle/cuttle.yaml:ro" \
  -v cuttle-data:/data \
  ghcr.io/ginuerzh/cuttle:latest
```

`/etc/cuttle/cuttle.yaml` is found when `/data/cuttle.yaml` does not exist. Use
`serve -c /etc/cuttle/cuttle.yaml` as the container command to be explicit.
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

The workflow builds the image from the `cuttle` context. Dependency versions are
the tags in `go.mod`; update them with `go get` and `go mod tidy`.

The workflow derives `VERSION` from the git ref: a `vX.Y.Z` tag uses the tag
name, and a branch build uses `<branch>-<short-sha>`. `COMMIT` and `BUILT_AT`
are passed alongside it, so `cuttle version` and `/api/health` identify the
exact build.
