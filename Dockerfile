# syntax=docker/dockerfile:1

ARG GO_VERSION=1.26

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN version="$VERSION"; \
    if [ -z "$version" ]; then version="$(tr -d '[:space:]' < VERSION)"; fi; \
    CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build \
      -trimpath \
      -ldflags "-s -w -X main.version=$version" \
      -o /out/pappice \
      ./cmd/pappice

RUN mkdir -p /out/root/data/uploads /out/root/backups /out/root/tmp && \
    chmod 1777 /out/root/tmp && \
    chown -R 10001:10001 /out/root/data /out/root/backups /out/root/tmp

FROM scratch

LABEL org.opencontainers.image.title="Pappice" \
      org.opencontainers.image.description="Small self-hosted chat-style support desk" \
      org.opencontainers.image.source="https://github.com/lallero-dev/pappice" \
      org.opencontainers.image.licenses="GPL-3.0-only"

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/pappice /pappice
COPY --from=build --chown=10001:10001 /out/root/data /data
COPY --from=build --chown=10001:10001 /out/root/backups /backups
COPY --from=build --chown=10001:10001 /out/root/tmp /tmp

ENV PAPPICE_ADDR=0.0.0.0:8388 \
    PAPPICE_DB=/data/pappice.db \
    PAPPICE_UPLOAD_DIR=/data/uploads \
    PAPPICE_BACKUP_DIR=/backups

EXPOSE 8388
VOLUME ["/data", "/backups"]
USER 10001:10001

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/pappice", "healthcheck"]

ENTRYPOINT ["/pappice"]
CMD ["serve"]
