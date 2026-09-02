# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /build_src/web/src
COPY web/src/package.json web/src/package-lock.json ./
RUN npm ci
COPY web/src/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
ARG VERSION_MAJOR=0
ARG VERSION_MINOR=1
ARG VERSION_PATCH=0
ARG VERSION_PRE_RELEASE=dev
ARG BUILD_CHANNEL=docker
ARG BUILD_COMMIT=unknown
ARG BUILD_TIMESTAMP=0
WORKDIR /build_src
RUN apk add --no-cache git make
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=web /build_src/web/dist ./web/dist
RUN GOOS="$TARGETOS" \
    GOARCH="$TARGETARCH" \
    GOARM="${TARGETVARIANT#v}" \
    make core \
    CURRENT_VERSION_MAJOR="$VERSION_MAJOR" \
    CURRENT_VERSION_MINOR="$VERSION_MINOR" \
    CURRENT_VERSION_PATCH="$VERSION_PATCH" \
    VERSION_PRE_RELEASE="$VERSION_PRE_RELEASE" \
    BUILD_CHANNEL="$BUILD_CHANNEL" \
    BUILD_TOOLCHAIN="${TARGETOS}_${TARGETARCH}${TARGETVARIANT:+_$TARGETVARIANT}" \
    COMMIT="$BUILD_COMMIT" \
    TIMESTAMP="$BUILD_TIMESTAMP"

FROM --platform=$BUILDPLATFORM alpine:3.23 AS runtime-files
RUN apk add --no-cache ca-certificates tzdata

FROM scratch
COPY --from=runtime-files /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=runtime-files /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /build_src/build/dist/proxygate /proxygate
ENV PROXYGATE_WEB_LISTEN_ADDRESS="[::]:8080" \
    PROXYGATE_SOCKS5_LISTEN_ADDRESS="127.0.0.1:1080" \
    PROXYGATE_DATABASE_PATH="/data/proxygate.db"
WORKDIR /data
VOLUME ["/data"]
EXPOSE 8080 1080
ENTRYPOINT ["/proxygate", "-config", "/data/config.json"]
