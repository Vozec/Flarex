# FlareX — multi-stage build
#
# Stage 1 (web): build the React SPA with pnpm → dist/ in the same layout
#    the Go embed directive expects (internal/admin/webui/dist).
# Stage 2 (builder): Go toolchain compiles both binaries, embedding the
#    SPA bundle from stage 1.
# Stage 3 (prod): Alpine runtime — only flarex + CA certs + nonroot user.
# Stage 4 (mock): distroless with only mockworker.
#
# Build with `docker build -t flarex .` or `make docker-ui`. The SPA is
# always included; disable it at runtime with `admin.ui: false`.

FROM node:22-alpine AS web
WORKDIR /src/web
# deps layer (cache). .npmrc carries `legacy-peer-deps=true` which is
# required for react-simple-maps v3 to install on React 19 — without it
# `npm install` errors with ERESOLVE.
COPY web/package.json web/package-lock.json* web/pnpm-lock.yaml* web/.npmrc ./
RUN npm ci --no-audit --no-fund --loglevel=error
# sources + build
COPY web/ ./
# Vite writes to ../internal/admin/webui/dist per vite.config.ts. We need
# that path to exist outside web/ for the Go embed in stage 2.
RUN mkdir -p /src/internal/admin/webui/dist && npm run build

# ============================================================================
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Overlay the freshly-built SPA from stage 1 onto the Go source tree so
# //go:embed picks it up.
COPY --from=web /src/internal/admin/webui/dist ./internal/admin/webui/dist
ARG VERSION=docker
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/flarex ./cmd/flarex \
 && CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/mockworker ./cmd/mockworker

# ============================================================================
FROM alpine:3 AS prod
RUN apk add --no-cache ca-certificates && \
    adduser -D -u 65532 -H nonroot && \
    mkdir -p /state /etc/flarex && chown nonroot:nonroot /state /etc/flarex
COPY --from=builder /out/flarex /flarex
# Bake a zero-placeholder config so containers boot with env-only config
# (FLX_HMAC_SECRET, FLX_TOKENS, etc). Overridable via bind mount.
COPY deploy/config.docker.yaml /etc/flarex/config.yaml
USER nonroot
# SOCKS5 + admin HTTP
EXPOSE 1080 9090
ENTRYPOINT ["/flarex"]
# `--deploy` auto-provisions the worker pool on a fresh state volume so
# a first `docker compose up` is self-contained (no need to `flarex
# deploy` separately). Override in compose if you manage deploys
# externally, e.g. `command: ["-c", "/etc/flarex/config.yaml", "server"]`.
CMD ["-c", "/etc/flarex/config.yaml", "server", "--deploy"]

# ============================================================================
FROM gcr.io/distroless/static-debian12:nonroot AS mock
COPY --from=builder /out/mockworker /mockworker
USER nonroot:nonroot
EXPOSE 8787
ENTRYPOINT ["/mockworker"]
CMD ["--addr", ":8787"]
