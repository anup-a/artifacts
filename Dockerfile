# The build stage runs on the builder's own architecture and Go cross-compiles
# to $TARGETARCH, so an arm64 Mac can build the amd64 image for a Hetzner box
# without emulating the toolchain (see deploy/Dockerfile.fly).
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/artifacts-server ./cmd/artifacts-server
RUN mkdir -p /out/data && chown 65532:65532 /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/artifacts-server /artifacts-server
COPY --from=build --chown=65532:65532 /out/data /data
VOLUME ["/data"]
ENV DATA_DIR=/data ADDR=:8080
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/artifacts-server"]
