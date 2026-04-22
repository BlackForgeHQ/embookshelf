# syntax=docker/dockerfile:1.7
# ---------- Stage 1: React build ----------
FROM oven/bun:1 AS assets
WORKDIR /app
COPY ui/package.json ui/bun.lock* ./ui/
RUN cd ui && bun install --frozen-lockfile
COPY ui ./ui
# sync-dist.ts writes to ../internal/staticfs/dist — ensure the parent
# exists before it runs.
RUN mkdir -p internal/staticfs && cd ui && bun run build

# ---------- Stage 2: Go build ----------
FROM golang:1.25-alpine AS gobuild
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=assets /app/internal/staticfs/dist ./internal/staticfs/dist
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
        -o /out/embookshelf ./cmd/embookshelf

# ---------- Stage 3: Runtime ----------
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=gobuild /out/embookshelf /app/embookshelf
EXPOSE 6060
USER nonroot:nonroot
ENTRYPOINT ["/app/embookshelf"]
