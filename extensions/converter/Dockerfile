# Build stage: dependency layer first so source edits don't re-download crates.
FROM rust:1.97-alpine AS build
RUN apk add --no-cache musl-dev
WORKDIR /src
COPY Cargo.toml Cargo.lock ./
RUN mkdir src && echo "fn main() {}" > src/main.rs && touch src/lib.rs \
    && cargo build --release --locked \
    && rm -rf src
COPY src ./src
RUN touch src/main.rs src/lib.rs && cargo build --release --locked

FROM alpine:3.20
COPY --from=build /src/target/release/embookshelf-converter /usr/local/bin/embookshelf-converter
EXPOSE 6070
USER nobody
ENTRYPOINT ["embookshelf-converter"]
