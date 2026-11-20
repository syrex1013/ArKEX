# syntax=docker/dockerfile:1

# ===== Stage 1: Build Next.js static export =====
FROM node:22-alpine AS web-builder
WORKDIR /app/web
COPY web/package.json web/package-lock.json* ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci --frozen-lockfile
COPY web/ ./
RUN npm run build:static

# ===== Stage 2: Build Go binary =====
FROM golang:1.26-alpine AS go-builder
WORKDIR /app
RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/root/go/pkg/mod \
    go mod download

COPY . .
COPY --from=web-builder /app/web/dist ./cmd/arkex/web/dist

RUN --mount=type=cache,target=/root/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.Version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)" \
    -tags embedui \
    -o /arkex \
    ./cmd/arkex

# ===== Stage 3: Minimal runtime =====
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata docker-cli
RUN addgroup -S arkex && adduser -S -G arkex arkex
USER arkex
COPY --from=go-builder /arkex /arkex
EXPOSE 8080
ENTRYPOINT ["/arkex"]
