# syntax=docker/dockerfile:1.7

# llm-gw depends on published module tags. Build from this directory:
#   docker build -t llm-gw .
#
# The GitHub Actions workflow uses this directory as the build context too.

FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /src/llm-gw/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY web ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
# Keep the default for CI; local builds in restricted networks can override:
#   --build-arg GOPROXY=https://goproxy.cn,direct --build-arg GOSUMDB=off
ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ENV GOPROXY=$GOPROXY \
    GOSUMDB=$GOSUMDB
WORKDIR /src/llm-gw
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/llm-gw/internal/admin/dist ./internal/admin/dist
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/llm-gw .

FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 llm-gw \
    && adduser -S -u 10001 -G llm-gw llm-gw \
    && mkdir -p /data \
    && chown -R llm-gw:llm-gw /data \
    && chmod 700 /data

ENV LLM_GW_DATA=/data \
    LLM_GW_ADMIN_LISTEN=:8788

WORKDIR /data
COPY --from=build /out/llm-gw /usr/local/bin/llm-gw

USER llm-gw
EXPOSE 8787 8788
VOLUME ["/data"]

# Zero-config first run:
#   docker run -d -p 8787:8787 -p 8788:8788 -v llm-gw-data:/data <image>
# /data holds the generated llm-gw.yaml and stats.sqlite. The admin password is
# printed once to the container logs. Set LLM_GW_ADMIN_PASSWORD before the
# first start to choose it. An existing config at /etc/llm-gw/llm-gw.yaml is
# still found through the legacy fallback; use `serve -c /etc/llm-gw/llm-gw.yaml`
# as the container command to select it explicitly.
ENTRYPOINT ["llm-gw"]
CMD ["serve"]
