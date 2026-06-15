FROM golang:1.25-alpine AS builder

WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/agent ./cmd/agent

FROM alpine:3.22

WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata \
  && adduser -D -H -u 10001 appuser
COPY --from=builder /out/agent ./agent

USER appuser
ENTRYPOINT ["/app/agent"]
