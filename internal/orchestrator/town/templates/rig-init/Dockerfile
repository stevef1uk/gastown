# Dockerfile for Go web server
# Template variables: {{PLAYWRIGHT_BASE_URL}}, {{WEB_PORT}}
# Multi-stage build: golang:1.22-alpine builder -> alpine:3.20 runtime
# The web app runs on HOST (not in Docker). Playwright container uses host networking to reach it.

# Stage 1: Builder
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/server/main.go ./cmd/server/
RUN CGO_ENABLED=0 go build -o /server ./cmd/server

# Stage 2: Runtime
FROM alpine:3.20
WORKDIR /app
COPY --from=builder /server /usr/local/bin/server
COPY web ./web
EXPOSE 8080
CMD ["server", "-addr", ":8080"]