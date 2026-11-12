# syntax=docker/dockerfile:1.7

# cuttle depends on published module tags. Build from this directory:
#   docker build -t cuttle .
#
# The GitHub Actions workflow uses this directory as the build context too.

FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /src/cuttle/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY web ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev
ARG COMMIT=
ARG BUILT_AT=
# Keep the default for CI; local builds in restricted networks can override:
#   --build-arg GOPROXY=https://goproxy.cn,direct --build-arg GOSUMDB=off
ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ENV GOPROXY=$GOPROXY \
    GOSUMDB=$GOSUMDB
WORKDIR /src/cuttle
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/cuttle/internal/admin/dist ./internal/admin/dist
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X github.com/ginuerzh/cuttle/internal/version.Version=${VERSION} -X github.com/ginuerzh/cuttle/internal/version.Commit=${COMMIT} -X github.com/ginuerzh/cuttle/internal/version.BuildDate=${BUILT_AT}" -o /out/cuttle .

FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 cuttle \
    && adduser -S -u 10001 -G cuttle cuttle \
    && mkdir -p /data \
    && chown -R cuttle:cuttle /data \
    && chmod 700 /data

# Keep the image on /data; the binary default is <user config dir>/cuttle.
ENV CUTTLE_DATA=/data \
    CUTTLE_ADMIN_LISTEN=:8788

WORKDIR /data
COPY --from=build /out/cuttle /usr/local/bin/cuttle

USER cuttle
EXPOSE 8787 8788
VOLUME ["/data"]

# Zero-config first run:
#   docker run -d -p 8787:8787 -p 8788:8788 -v cuttle-data:/data <image>
# /data holds the generated cuttle.yaml and stats.sqlite. The admin password is
# printed once to the container logs. Set CUTTLE_ADMIN_PASSWORD before the
# first start to choose it. An existing config at /etc/cuttle/cuttle.yaml is
# still found through the fallback path; use `serve -c /etc/cuttle/cuttle.yaml`
# as the container command to select it explicitly.
ENTRYPOINT ["cuttle"]
CMD ["serve"]
