# syntax=docker/dockerfile:1
#
# Build a static linux/amd64 binary at the conservative x86-64-v1 baseline.
# The runtime image contains no Node.js, shell, package manager, or database
# server. Only the Go binary, CA certificates, and tzdata are required.

ARG GO_VERSION=1.27.1

FROM golang:${GO_VERSION}-alpine AS build

WORKDIR /src
RUN apk add --no-cache ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN mkdir -p /out/data \
    && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1 \
       go build -trimpath -buildvcs=false \
       -ldflags="-s -w -X main.version=${VERSION}" \
       -o /out/libreai ./cmd/libreai \
    && chmod 0700 /out/data \
    && chown 65532:65532 /out/data

FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.title="libreAI" \
      org.opencontainers.image.description="Lightweight self-hosted AI chat" \
      org.opencontainers.image.licenses="MIT"

COPY --from=build /out/libreai /usr/local/bin/libreai
COPY --from=build --chown=65532:65532 /out/data /data

# Listening on 0.0.0.0 is safe here only because the published port is bound
# to host loopback by compose.yaml. Provider configuration is supplied at
# runtime through env_file/environment and is never baked into the image.
ENV HOST=0.0.0.0 \
    PORT=8080 \
    DATABASE_PATH=/data/libreai.db

EXPOSE 8080
USER 65532:65532

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/usr/local/bin/libreai", "-healthcheck"]

ENTRYPOINT ["/usr/local/bin/libreai"]
