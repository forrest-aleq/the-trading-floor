FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /floor ./cmd/floor
RUN CGO_ENABLED=0 GOOS=linux go build -o /ctl ./cmd/ctl
RUN CGO_ENABLED=0 GOOS=linux go build -o /backfill ./cmd/backfill

FROM scratch
COPY --from=builder /floor /floor
COPY --from=builder /ctl /ctl
COPY --from=builder /backfill /backfill
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/store/migrations /migrations

ENV TRADING_FLOOR_MIGRATION_PATH=/migrations/001_init.sql

ENTRYPOINT ["/floor"]
