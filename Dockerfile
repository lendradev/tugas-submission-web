# Build binary statis untuk linux/arm64 & linux/amd64.
#golang:1.26-alpine AS build
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

# Cache dependensi dilayer terpisah agar build ulang cepat.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 menghasilkan binary statis tanpa libc -> cocok untuk ARM64 kecil.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/tugas-submission .

# ---------------------------------------------------------------------------

FROM alpine:3.24

RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 -h /app tugas

WORKDIR /app
COPY --from=build /out/tugas-submission /app/tugas-submission

# Folder data (database + staging) ditulis oleh pengguna non-root.
RUN mkdir -p /data && chown tugas:tugas /data
VOLUME ["/data"]

ENV TS_ADDR=0.0.0.0:8080 \
    TS_DATA_DIR=/data

USER tugas
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- "http://127.0.0.1:8080/login" >/dev/null 2>&1 || exit 1

ENTRYPOINT ["/app/tugas-submission"]
