ARG BUN_VERSION=1.3.14
ARG GO_VERSION=1.26.2
ARG ALPINE_VERSION=3.22.4

FROM oven/bun:${BUN_VERSION}-alpine AS ui-builder

WORKDIR /ui
COPY ui/package.json ui/bun.lock ./
RUN bun install --frozen-lockfile
COPY ui/ ./
RUN bun run build

FROM golang:${GO_VERSION}-alpine AS go-builder

RUN apk add --no-cache git

WORKDIR /build

COPY services/admin/go.mod services/admin/go.sum ./
RUN go mod download

COPY services/admin/ ./
RUN CGO_ENABLED=0 go build -o admin .

FROM alpine:${ALPINE_VERSION}

RUN apk add --no-cache ca-certificates bash \
    && mkdir -p /app/configs /app/data

COPY --from=go-builder /build/admin /app/admin
COPY --from=ui-builder /ui/dist /app/ui

WORKDIR /app
# 不使用 USER appuser：服务器用户 uid=1001，Dockerfile 中 appuser uid=1000，
# .wework/ 下的文件由 root 或 uid=1001 创建，appuser(1000) 无法读写。
# 安全保护由 docker-compose 中 cap_drop:ALL + no-new-privileges 实现。
ENTRYPOINT ["/app/admin"]
