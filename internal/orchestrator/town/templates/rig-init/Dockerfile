# Dockerfile for Go web server
# Template variables: {{PLAYWRIGHT_BASE_URL}}, {{WEB_PORT}}
# Multi-stage build: golang:1.22-alpine builder -> alpine:3.20 runtime
# The web app runs on HOST (not in Docker). Playwright container reaches it via host.docker.internal.

# Stage 1: Builder
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 go build -o /server {{GO_BUILD_TARGET}}
RUN mkdir -p web

# Stage 2: Runtime
FROM alpine:3.20
RUN apk add --no-cache ca-certificates curl
WORKDIR /app
COPY --from=builder /server /usr/local/bin/server
COPY --from=builder /app/web ./web
EXPOSE {{APP_PORT}}
CMD ["server", "-addr", ":{{APP_PORT}}"]
