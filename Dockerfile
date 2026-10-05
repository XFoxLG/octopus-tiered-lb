# =============================================================================
# Build stage for frontend
# =============================================================================
FROM --platform=$BUILDPLATFORM node:20-alpine AS frontend-builder

WORKDIR /build

# Install pnpm
RUN corepack enable

# Copy frontend package files
COPY web/package.json web/pnpm-lock.yaml ./

# Install dependencies
RUN pnpm install --frozen-lockfile

# Copy frontend source
COPY web/ ./

# Build frontend with version injected
ARG APP_VERSION=dev
ARG GITHUB_REPO=https://github.com/XFoxLG/octopus-tiered-lb
RUN NEXT_PUBLIC_APP_VERSION="${APP_VERSION}" NEXT_PUBLIC_GITHUB_REPO="${GITHUB_REPO}" pnpm build

# =============================================================================
# Build stage for Go binary
# =============================================================================
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS go-builder

WORKDIR /build

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Copy frontend build output to static directory
COPY --from=frontend-builder /build/out ./static/out

# Ensure _not-found has a placeholder file for go:embed
RUN if [ -d "static/out/_not-found" ] && [ ! -f "static/out/_not-found/.keep" ]; then \
        echo 'placeholder for go:embed' > static/out/_not-found/.keep; \
    fi

# Build arguments for version info
ARG APP_VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_TIME=unknown
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

# Build the binary
RUN set -eux; \
    target_os="${TARGETOS:-linux}"; \
    target_arch="${TARGETARCH:-amd64}"; \
    export CGO_ENABLED=0 GOOS="${target_os}" GOARCH="${target_arch}"; \
    if [ "${target_arch}" = "arm" ] && [ -n "${TARGETVARIANT}" ]; then \
        export GOARM="${TARGETVARIANT#v}"; \
    fi; \
    go build \
      -ldflags="-X 'github.com/lingyuins/octopus/internal/conf.Version=${APP_VERSION}' \
                -X 'github.com/lingyuins/octopus/internal/conf.Commit=${GIT_COMMIT}' \
                -X 'github.com/lingyuins/octopus/internal/conf.BuildTime=${BUILD_TIME}' \
                -X 'github.com/lingyuins/octopus/internal/conf.Author=lingyu' \
                -s -w" \
      -tags=jsoniter \
      -o octopus \
      .

# =============================================================================
# Runtime stage
# =============================================================================
FROM alpine:3.20
ARG APP_VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_TIME=unknown
LABEL org.opencontainers.image.version="${APP_VERSION}" \
      org.opencontainers.image.revision="${GIT_COMMIT}" \
      org.opencontainers.image.created="${BUILD_TIME}"

# Install runtime dependencies (su-exec for non-root user switching in entrypoint)
RUN apk add --no-cache ca-certificates tzdata su-exec

# Set default timezone for the container.
# Override with -e TZ=... at docker run or environment in compose.
ENV TZ=Asia/Shanghai

# Create non-root user
RUN addgroup -g 1000 octopus && \
    adduser -u 1000 -G octopus -s /bin/sh -D octopus

WORKDIR /app

# Copy binary
COPY --from=go-builder /build/octopus .

# Copy entrypoint script (handles chown of mounted data dir + drops to octopus user)
COPY scripts/dockerfiles/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Create data directory
RUN mkdir -p /app/data && chown -R octopus:octopus /app

# 容器以 root 启动 entrypoint.sh，由其 chown /app/data 修复挂载目录权限后
# 用 su-exec 降权到 octopus(1000) 运行主进程（issue #198 权限只读循环重启修复）。
# 不在此处设置 USER，否则 entrypoint 无法 chown 宿主挂载的只读目录。

# Expose port
EXPOSE 8080

# Set default data directory
ENV OCTOPUS_DATA_DIR=/app/data

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/api/v1/bootstrap/status || exit 1

# Run via entrypoint.sh (chowns /app/data, then drops to octopus user via su-exec)
ENTRYPOINT ["/entrypoint.sh"]
