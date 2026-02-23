# Build stage
FROM golang:1.21-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /opnsense-lb-controller ./cmd/opnsense-lb-controller

# Runtime stage (minimal image with CA certs for OPNsense HTTPS)
FROM alpine:3.19
RUN apk add --no-cache ca-certificates && \
	adduser -D -u 65532 nonroot
COPY --from=builder /opnsense-lb-controller /opnsense-lb-controller
USER nonroot
ENTRYPOINT ["/opnsense-lb-controller"]
