FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/artifacts-server ./cmd/artifacts-server
RUN mkdir -p /out/data && chown 65532:65532 /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/artifacts-server /artifacts-server
COPY --from=build --chown=65532:65532 /out/data /data
VOLUME ["/data"]
ENV DATA_DIR=/data ADDR=:8080
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/artifacts-server"]
