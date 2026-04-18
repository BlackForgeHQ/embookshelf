# syntax=docker/dockerfile:1.7
# ---------- Stage 1: Tailwind CSS build ----------
FROM node:22-alpine AS assets
WORKDIR /app
COPY package.json ./
RUN npm install --no-audit --no-fund
COPY web ./web
COPY internal ./internal
RUN mkdir -p internal/staticfs/static && \
    npx @tailwindcss/cli -i web/src/styles.css -o internal/staticfs/static/app.css --minify && \
    wget -qO internal/staticfs/static/htmx.min.js https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js

# ---------- Stage 2: Go build ----------
FROM golang:1.24-alpine AS gobuild
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=assets /app/internal/staticfs/static ./internal/staticfs/static
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go tool templ generate && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
        -o /out/embookshelf ./cmd/embookshelf

# ---------- Stage 3: Runtime ----------
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=gobuild /out/embookshelf /app/embookshelf
EXPOSE 6060
USER nonroot:nonroot
ENTRYPOINT ["/app/embookshelf"]
