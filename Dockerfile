# Stage 1: Build CSS with Tailwind standalone CLI
FROM alpine:3.21 AS css
RUN apk add --no-cache curl
ARG TAILWIND_VERSION=v4.1.6
RUN curl -sL "https://github.com/tailwindlabs/tailwindcss/releases/download/${TAILWIND_VERSION}/tailwindcss-linux-x64" -o /tailwindcss && \
    chmod +x /tailwindcss
COPY input.css /input.css
RUN /tailwindcss -i /input.css -o /styles.css --minify

# Stage 2: Build Go binary
FROM golang:1.26-alpine AS builder
RUN apk add --no-cache curl git
RUN go install github.com/a-h/templ/cmd/templ@latest

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=css /styles.css assets/dist/styles.css

RUN templ generate
RUN go build -ldflags="-s -w -X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo '0.0.0-dev')" -o admin-ui .

# Stage 3: Minimal runtime image
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
RUN adduser -D -u 1000 admin
USER admin
WORKDIR /app
COPY --from=builder /build/admin-ui .
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD ["wget", "-q", "--tries=1", "--spider", "http://localhost:8080/health"]
ENTRYPOINT ["/app/admin-ui"]
