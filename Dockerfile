FROM golang:1.21-alpine AS builder

WORKDIR /app

# Copy go mod and sum files
COPY go.mod ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o rollout-helper

# Use a minimal alpine image for the final container
FROM alpine:3.19

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /app/rollout-helper .

# Run as non-root user
RUN adduser -D -g '' appuser
USER appuser

ENTRYPOINT ["/app/rollout-helper"] 