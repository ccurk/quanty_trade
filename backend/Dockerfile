# Build Stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache gcc musl-dev

# Copy go mod and sum files
COPY backend/go.mod backend/go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY backend/ ./

# Build the application
RUN CGO_ENABLED=1 GOOS=linux go build -a -installsuffix cgo -o main cmd/main.go

# Final Stage
FROM python:3.11-alpine

WORKDIR /app

# Install system dependencies for Go and Python strategies
RUN apk add --no-cache libc6-compat ca-certificates

# Copy the binary from builder
COPY --from=builder /app/main .

# Create directory for strategies (if they are outside backend)
# In this project, Python strategies are likely in a specific folder or managed by Go
# We'll copy the entire backend folder to ensure internal paths work
COPY backend/ ./

# Expose backend port
EXPOSE 8080

# Run the backend
CMD ["./main"]
