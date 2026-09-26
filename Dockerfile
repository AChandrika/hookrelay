# One Dockerfile for every Go program. Each final stage is a tiny image with a
# single static binary: `docker compose` picks the stage with `target:`.

FROM golang:1 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
# CGO_ENABLED=0 gives static binaries that run on a distroless base with no libc.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/...
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOBIN=/out go install github.com/pressly/goose/v3/cmd/goose@latest

# distroless/static: no shell, no package manager, runs as a non-root user.
# Less inside the image means less to patch and less for an attacker to use.
FROM gcr.io/distroless/static-debian12:nonroot AS api
COPY --from=build /out/api /api
ENV ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["/api"]

FROM gcr.io/distroless/static-debian12:nonroot AS worker
COPY --from=build /out/worker /worker
ENV METRICS_ADDR=:9091
EXPOSE 9091
ENTRYPOINT ["/worker"]

FROM gcr.io/distroless/static-debian12:nonroot AS mockreceiver
COPY --from=build /out/mockreceiver /mockreceiver
ENV ADDR=:8090
EXPOSE 8090
ENTRYPOINT ["/mockreceiver"]

# Runs migrations once and exits. goose reads its settings from GOOSE_* variables.
FROM gcr.io/distroless/static-debian12:nonroot AS migrate
COPY --from=build /out/goose /goose
COPY migrations /migrations
ENV GOOSE_DRIVER=postgres GOOSE_MIGRATION_DIR=/migrations
ENTRYPOINT ["/goose", "up"]

FROM gcr.io/distroless/static-debian12:nonroot AS eval
COPY --from=build /out/eval /eval
COPY evals /evals
WORKDIR /
ENTRYPOINT ["/eval"]
