# workwork-docker-proxy:docker API 精确白名单反代(见 main.go 头注释)。
# 单文件无第三方依赖;上游 unix socket,仅容器内网监听。
ARG GO_VERSION=1.26.9
ARG ALPINE_VERSION=3.24.1

FROM golang:${GO_VERSION}-alpine AS build
# 镜像源可覆盖(默认 aliyun,个人基建的国内网络现实;换环境时 build-arg 覆盖)。
ARG APK_MIRROR=mirrors.aliyun.com
ARG GOPROXY=https://goproxy.cn,direct
WORKDIR /src
RUN sed -i "s#dl-cdn.alpinelinux.org#${APK_MIRROR}#g" /etc/apk/repositories \
    && apk add --no-cache git ca-certificates
ENV GOPROXY=${GOPROXY}
COPY go.mod main.go ./
ARG GIT_VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${GIT_VERSION}" -o /out/docker-proxy .

FROM alpine:${ALPINE_VERSION}
ARG APK_MIRROR=mirrors.aliyun.com
RUN sed -i "s#dl-cdn.alpinelinux.org#${APK_MIRROR}#g" /etc/apk/repositories \
    && apk add --no-cache wget \
    && adduser -D -u 10001 proxyuser
COPY --from=build /out/docker-proxy /usr/local/bin/docker-proxy
ARG GIT_VERSION=dev
LABEL org.opencontainers.image.version="${GIT_VERSION}" \
      org.opencontainers.image.description="docker API whitelist proxy for workwork admin"
# 以 root 运行是必须的:docker.sock 属主 root:docker(0660),非 root 用户需要
# group_add 宿主 GID(GID 随发行版变化,compose 里难以稳定配置)。进程无任何
# capability(cap_drop:ALL),仅能连接挂载进来的 socket 文件。
USER root
EXPOSE 2375
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:2375/_ping || exit 1
ENTRYPOINT ["/usr/local/bin/docker-proxy"]
