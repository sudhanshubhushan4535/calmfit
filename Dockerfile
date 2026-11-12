# syntax=docker/dockerfile:1
# CalmFit images (D49). One build stage cross-compiles every binary for the target platform on the
# build machine's native Go (no QEMU), then each target copies only what it runs.
#   docker buildx build --platform linux/arm64 --target api .     (make images)
# Targets: api (also migrate, seed, reminders), worker, relay, backup (cmd/backup + restore-test on
# the postgres:16 client tools, D43).

ARG GO_VERSION=1.27.1

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG TARGETOS TARGETARCH
# Compile parallelism. 2 keeps peak memory inside Docker Desktop's 3.8 GB on the Mac, where
# unbounded parallel builds for two platforms got the VM's containers OOM-killed (D49).
ARG GO_BUILD_P=2
# Static binaries; timetzdata embeds the zone database so Asia/Kolkata never depends on the image.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -p ${GO_BUILD_P} -trimpath -tags timetzdata -ldflags="-s -w" -o /out/ \
      ./cmd/api ./cmd/worker ./cmd/relay ./cmd/migrate ./cmd/seed ./cmd/reminders ./cmd/backup ./cmd/restore-test

# distroless static: CA certificates (Google sign-in, Web Push, LLM API), no shell, runs as nonroot.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
WORKDIR /app
COPY config ./config

FROM runtime AS api
COPY migrations ./migrations
COPY seed ./seed
COPY --from=build /out/api /out/migrate /out/seed /out/reminders /usr/local/bin/
EXPOSE 8080 9101
ENTRYPOINT ["/usr/local/bin/api"]

FROM runtime AS worker
COPY --from=build /out/worker /usr/local/bin/
EXPOSE 9102
ENTRYPOINT ["/usr/local/bin/worker"]

FROM runtime AS relay
COPY --from=build /out/relay /usr/local/bin/
ENTRYPOINT ["/usr/local/bin/relay"]

# pg_dump/pg_restore 16 match the server (D43); PG_BIN makes the backup code call them directly.
FROM postgres:16 AS backup
COPY --from=build /out/backup /out/restore-test /usr/local/bin/
ENV PG_BIN=/usr/lib/postgresql/16/bin
WORKDIR /backups
ENTRYPOINT ["/usr/local/bin/backup", "-dir", "/backups"]
