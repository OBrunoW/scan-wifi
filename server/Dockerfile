FROM golang:1.22.12-bookworm AS build
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/scan-wifi ./cmd/server

FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends iputils-ping \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --system --no-create-home --shell /usr/sbin/nologin scanwifi
COPY --from=build /out/scan-wifi /usr/local/bin/scan-wifi
USER scanwifi
ENTRYPOINT ["/usr/local/bin/scan-wifi"]
