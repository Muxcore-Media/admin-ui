FROM golang:1.26-alpine AS builder
RUN apk add --no-cache curl git
RUN go install github.com/a-h/templ/cmd/templ@latest

# Copy dependencies alongside so replace directives resolve
COPY core/ /build/core/
COPY contracts-media-admin/ /build/contracts-media-admin/
COPY admin-ui/ /build/admin-ui/

WORKDIR /build/admin-ui
RUN go mod download

# Use pre-built CSS from the host
COPY admin-ui/assets/dist/styles.css assets/dist/styles.css
RUN templ generate

RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /admin-ui .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata curl
RUN adduser -D -u 1000 admin
USER admin
WORKDIR /app
COPY --from=builder /admin-ui .
EXPOSE 8082
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD ["sh", "-c", "curl -sf http://localhost:8082/health > /dev/null 2>&1"]
ENTRYPOINT ["/app/admin-ui"]
