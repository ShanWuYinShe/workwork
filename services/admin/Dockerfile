ARG BUN_VERSION=1.3.14
ARG GO_VERSION=1.26.8
ARG ALPINE_VERSION=3.24.1
# 镜像源可覆盖(默认 aliyun;换环境 build-arg 覆盖,compose 已传)。
ARG APK_MIRROR=mirrors.aliyun.com

FROM oven/bun:${BUN_VERSION}-alpine AS ui-builder

WORKDIR /ui
COPY services/admin/ui/package.json services/admin/ui/bun.lock ./
RUN bun install --frozen-lockfile
COPY services/admin/ui/ ./
RUN bun run build

FROM golang:${GO_VERSION}-alpine AS go-builder
ARG APK_MIRROR

RUN sed -i "s#dl-cdn.alpinelinux.org#${APK_MIRROR}#g" /etc/apk/repositories \
    && apk add --no-cache git

WORKDIR /build

ARG GOPROXY
ENV GOPROXY=${GOPROXY:-https://goproxy.cn,direct}

COPY services/admin/go.mod services/admin/go.sum ./
RUN go mod download

COPY services/admin/ ./
# -trimpath -ldflags "-s -w": 去掉本地路径与符号表(与 media/music 镜像一致)。
# GIT_VERSION(workwork git describe)由编排层传入,烙进二进制并经
# /api/admin/health 与启动日志暴露;未传时为 "dev"。
ARG GIT_VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${GIT_VERSION}" -o admin .

FROM alpine:${ALPINE_VERSION}
ARG APK_MIRROR

# RUN_UID 与服务器工作用户 uid 对齐(默认 1001,同 .wework/ 属主):
# 镜像内建非 root 用户后,脱离 docker-compose 直接跑也不会以 root 持有
# docker.sock;compose 的 user: "1001:1001" 作为显式覆盖保持行为一致。
ARG RUN_UID=1001
RUN sed -i "s#dl-cdn.alpinelinux.org#${APK_MIRROR}#g" /etc/apk/repositories \
    && apk add --no-cache ca-certificates bash \
    && adduser -D -u ${RUN_UID} appuser \
    && mkdir -p /app/configs /app/data \
    && chown -R appuser:appuser /app

COPY --from=go-builder /build/admin /app/admin
COPY --from=ui-builder /ui/dist /app/ui
# 配置模板由 admin 消费(启动时渲染到 .wework/generated/),随镜像分发:
# 模板版本与二进制版本原子对应,回滚镜像即回滚模板。
COPY services/admin/configs/ /app/configs/

# ARG 按阶段作用域:最终阶段需重新声明才能用于 LABEL。
ARG GIT_VERSION=dev
LABEL org.opencontainers.image.revision="${GIT_VERSION}"

WORKDIR /app
USER appuser
ENTRYPOINT ["/app/admin"]
