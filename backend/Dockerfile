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

# Install Python runtime dependencies for strategies
RUN apk add --no-cache ca-certificates
COPY strategies/requirements.txt /tmp/strategy-requirements.txt
RUN pip install --no-cache-dir -r /tmp/strategy-requirements.txt

# Copy the binary from builder
COPY --from=builder /app/main .

# Create directory for strategies (if they are outside backend)
# In this project, Python strategies are likely in a specific folder or managed by Go
# We'll copy the entire backend folder to ensure internal paths work
COPY backend/ ./
COPY strategies/ ./strategies

# Expose backend port
EXPOSE 8080

# 台账 #115:光有诚实的健康端点还不够,得有人去问它,否则"Redis 死了"依然只活在
# server.log 里。有了这一条,DB 或 Redis 一塌,docker ps / portainer 直接显示
# (unhealthy),约 30-60s 内可见,不用 grep 日志。
# wget 是 alpine 自带的 busybox applet(实测 /usr/bin/wget,BusyBox v1.37.0),不额外装包。
# 注意:docker 不会因 unhealthy 重启容器(restart:always 只看退出码),所以这纯粹是
# 观测信号,不会把所有者的重启/恢复流程卡住。
HEALTHCHECK --interval=30s --timeout=5s --start-period=90s --retries=2 \
  CMD wget -q -O /dev/null "http://127.0.0.1:${SERVER_PORT:-8080}/api/health" || exit 1

# Run the backend
CMD ["./main"]
