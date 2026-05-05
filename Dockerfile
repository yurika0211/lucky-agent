# Build stage
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git make

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
    -o luckyharness ./cmd/lh

# Runtime stage
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /app/luckyharness /usr/local/bin/luckyharness
COPY docker/prod-entrypoint.sh /usr/local/bin/prod-entrypoint.sh
RUN chmod +x /usr/local/bin/prod-entrypoint.sh

RUN mkdir -p /etc/luckyharness /var/lib/luckyharness

VOLUME ["/etc/luckyharness", "/var/lib/luckyharness"]

EXPOSE 9090

ENTRYPOINT ["prod-entrypoint.sh"]
CMD ["serve"]
