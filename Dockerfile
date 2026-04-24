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
FROM golang:1.26-alpine AS gobuild
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=assets /app/internal/staticfs/dist ./internal/staticfs/dist
ARG VERSION=dev
ARG COMMIT=unknown
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath \
        -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
        -o /out/embookshelf ./cmd/embookshelf

# ---------- Stage 3: Runtime ----------
FROM gcr.io/distroless/static-debian12:nonroot
ARG VERSION=dev
ARG COMMIT=unknown
LABEL org.opencontainers.image.title="embookshelf" \
      org.opencontainers.image.description="Self-hosted, multi-user digital library" \
      org.opencontainers.image.source="https://github.com/BlackForgeHQ/embookshelf" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.licenses="MIT"
WORKDIR /app
COPY --from=gobuild /out/embookshelf /app/embookshelf
EXPOSE 6060
USER nonroot:nonroot
ENTRYPOINT ["/app/embookshelf"]
