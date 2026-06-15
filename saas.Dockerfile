FROM node:20-alpine AS frontend

WORKDIR /src/web-frontend
COPY web-frontend/package*.json ./
RUN npm ci
COPY web-frontend/ ./
RUN npm run build

FROM golang:1.25-alpine AS builder

WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/saas ./cmd/saas

FROM alpine:3.22

WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata \
  && adduser -D -H -u 10001 appuser
COPY --from=builder /out/saas ./saas
COPY --from=frontend /src/web-frontend/dist ./web-frontend/dist
COPY config.yaml ./config.yaml

USER appuser
EXPOSE 8080
ENTRYPOINT ["/app/saas"]
