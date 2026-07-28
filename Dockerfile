# Multi-stage build → distroless, non-root static binary.
FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Semver without a leading "v" (the release workflow strips it). Left at the
# buildinfo default when unset, so a plain `docker build` still produces a
# working image — it just reports itself as "dev".
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X github.com/sunnysystems/sunshine-host-sampling-controller/internal/buildinfo.Version=${VERSION}" \
    -o /out/controller ./cmd/controller

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/controller /controller
USER 65532:65532
ENTRYPOINT ["/controller"]
