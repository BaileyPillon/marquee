# syntax=docker/dockerfile:1
#
# Marquee: a small container image. The final image is `FROM scratch`: the one
# static binary, CA certificates, a LICENSE file, an empty /config and /tmp.
# There is no shell, no package manager and no libc.
#
# Build from the repository root (the build context is the whole repo):
#
#   docker build -f deploy/Dockerfile --build-arg VERSION=$(cat VERSION) -t marquee .
#
# Multi-arch (what the release workflow does):
#
#   docker buildx build -f deploy/Dockerfile \
#     --platform linux/amd64,linux/arm64,linux/arm/v7 \
#     --build-arg VERSION=0.1.0 -t ghcr.io/baileypillon/marquee:0.1.0 --push .
#
# The Node and Go stages always run on the machine doing the build
# ($BUILDPLATFORM) and cross-compile for the target. Go needs no C compiler
# (CGO is off), so no emulation (QEMU) is involved.

# ---- 1. web UI ----------------------------------------------------------------
FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /src/web

# Dependencies first, so editing a component does not reinstall them.
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci

COPY web/ ./
RUN npm run build

# ---- 2. the binary --------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ENV CGO_ENABLED=0 \
    GOTOOLCHAIN=local \
    GOFLAGS=-mod=readonly
RUN apk add --no-cache ca-certificates

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY tools ./tools
# web/embed.go holds the //go:embed line; the UI itself comes from the stage above.
COPY web/embed.go ./web/embed.go
COPY --from=web /src/web/dist ./web/dist

# Brotli-compress the UI in place. This runs for the build machine, so no
# GOOS/GOARCH here.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go run ./tools/precompress web/dist

ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
# -tags timetzdata embeds Go's time zone database in the binary (about 400 KiB),
# which is what makes the TZ variable work in an image with no /usr/share/zoneinfo.
# GOARM comes from the platform variant: linux/arm/v7 gives GOARM=7; it is empty
# (and ignored) for amd64 and arm64.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" GOARM="${TARGETVARIANT#v}" \
    go build -trimpath -buildvcs=false -tags timetzdata \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/marquee ./cmd/marquee

# Everything the final image holds, laid out as it will appear.
COPY LICENSE /rootfs/LICENSE
RUN mkdir -p /rootfs/etc/ssl/certs /rootfs/tmp /skel/config \
 && cp /etc/ssl/certs/ca-certificates.crt /rootfs/etc/ssl/certs/ca-certificates.crt \
 && cp /out/marquee /rootfs/marquee \
 && chmod 0755 /rootfs/marquee \
 && chmod 1777 /rootfs/tmp \
 && chmod 0750 /skel/config

# ---- 3. the image -----------------------------------------------------------------
FROM scratch

ARG VERSION=dev
LABEL org.opencontainers.image.title="Marquee" \
      org.opencontainers.image.description="One small self-hosted app for movies, TV and music: requests, sources, downloads and import." \
      org.opencontainers.image.url="https://github.com/BaileyPillon/marquee" \
      org.opencontainers.image.source="https://github.com/BaileyPillon/marquee" \
      org.opencontainers.image.documentation="https://github.com/BaileyPillon/marquee/tree/main/docs" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}"

COPY --from=build /rootfs/ /
# /config is owned by the default user, so a named volume (which copies the
# image's ownership on first use) is writable without any extra step. A bind
# mount keeps the ownership of the host folder; see docs/install.md.
COPY --from=build --chown=65532:65532 /skel/config /config

# 65532 is the conventional "nonroot" id. Scratch has no /etc/passwd, so the
# usual PUID/PGID variables are NOT supported: there is no entrypoint script to
# read them. To run as another user, set `user: "1000:1000"` in compose (or
# `docker run --user 1000:1000`) and make sure that user owns the /config folder.
USER 65532:65532

ENV MARQUEE_CONFIG=/config \
    MARQUEE_ADDR=:6464

EXPOSE 6464
VOLUME /config

# There is no shell and no curl in this image, so a health check has to be a
# subcommand of the binary itself. Marquee does not have one yet (see the
# "marquee healthcheck" note in the M11 report). Until it does, there is none.
# When it lands, replace the next line with:
#   HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
#     CMD ["/marquee", "healthcheck"]
HEALTHCHECK NONE

ENTRYPOINT ["/marquee"]
