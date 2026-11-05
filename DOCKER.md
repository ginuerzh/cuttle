# Docker

`llm-gw` uses local `replace` directives for three sibling modules, so the
Docker build context is the parent directory that contains:

```text
core/
x/
llm-api-converter/
llm-gw/
```

Build from that parent directory:

```bash
docker build -f llm-gw/Dockerfile -t llm-gw .
```

Run with a mounted config:

```bash
docker run -d --name llm-gw \
  -p 8787:8787 -p 8788:8788 \
  -v "$PWD/llm-gw.yaml:/etc/llm-gw/llm-gw.yaml:ro" \
  -v llm-gw-data:/data \
  llm-gw
```

`/data` is the working directory, so a relative `stats.path` such as
`stats.sqlite` persists in the volume.

The image runs as UID/GID `10001` (`llm-gw`). Mounted configs must be readable
by that user, and a bind-mounted `/data` must be writable by it.

## GitHub Actions

`.github/workflows/docker.yml` builds `linux/amd64` and `linux/arm64` images and
pushes them to GHCR on pushes to `main` and `v*` tags.

The workflow checks out the public sibling repositories at pinned refs. Update
the pinned refs when the sibling modules change.
