# syntax=docker/dockerfile:1.7

# llm-gw depends on sibling modules through local `replace` directives:
#   ../core, ../x, ../llm-api-converter
# Build from the parent directory that contains all four repositories:
#   docker build -f llm-gw/Dockerfile .
#
# The GitHub Actions workflow checks the siblings out into the same layout.

FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /src/llm-gw/web
COPY llm-gw/web/package.json llm-gw/web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY llm-gw/web ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
# Keep the default for CI; local builds in restricted networks can override:
#   --build-arg GOPROXY=https://goproxy.cn,direct --build-arg GOSUMDB=off
ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ENV GOPROXY=$GOPROXY \
    GOSUMDB=$GOSUMDB
WORKDIR /src
COPY llm-gw/go.mod llm-gw/go.sum ./llm-gw/
COPY core ./core
COPY x ./x
COPY llm-api-converter ./llm-api-converter
WORKDIR /src/llm-gw
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY llm-gw ./
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
    && chown -R llm-gw:llm-gw /data

WORKDIR /data
COPY --from=build /out/llm-gw /usr/local/bin/llm-gw

USER llm-gw
EXPOSE 8787 8788
VOLUME ["/data"]

# Mount the config read-only:
#   -v "$PWD/llm-gw.yaml:/etc/llm-gw/llm-gw.yaml:ro"
# A relative stats path such as `stats.sqlite` is written under /data.
ENTRYPOINT ["llm-gw"]
CMD ["serve", "-c", "/etc/llm-gw/llm-gw.yaml"]
