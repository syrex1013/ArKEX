# ===== Stage 1: Build Next.js static export =====
FROM node:22-alpine AS web-builder
WORKDIR /app/web
COPY web/package.json web/package-lock.json* ./
RUN npm ci --frozen-lockfile
COPY web/ ./
RUN npm run build:static

# ===== Stage 2: Build Go binary =====
FROM golang:1.26-alpine AS go-builder
WORKDIR /app
# Install build deps
RUN apk add --no-cache gcc musl-dev git

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Copy web dist from stage 1
COPY --from=web-builder /app/web/dist ./cmd/arkex/web/dist

RUN CGO_ENABLED=0 GOOS=linux go build \
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
