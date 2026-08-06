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

# RUN_UID 与服务器工作用户 uid 对齐(默认 1001,同 .wework/ 属主):
# 镜像内建非 root 用户后,脱离 docker-compose 直接跑也不会以 root 持有
# docker.sock;compose 的 user: "1001:1001" 作为显式覆盖保持行为一致。
ARG RUN_UID=1001
RUN apk add --no-cache ca-certificates bash \
    && adduser -D -u ${RUN_UID} appuser \
    && mkdir -p /app/configs /app/data \
    && chown -R appuser:appuser /app

COPY --from=go-builder /build/admin /app/admin
COPY --from=ui-builder /ui/dist /app/ui

WORKDIR /app
USER appuser
ENTRYPOINT ["/app/admin"]
