ARG ALPINE_VERSION=3.24.1
# 镜像源可覆盖(默认 aliyun,国内网络现实;换环境 build-arg 覆盖)。
ARG APK_MIRROR=mirrors.aliyun.com
# 锁定 26.6.27：26.7.x 的 REALITY 握手协议与旧客户端不兼容（26.3.27 认证
# 握手被服务端直接关闭，官方 issue #6477 closed as not planned），客户端
# 全员升级前不可升级服务端。
ARG XRAY_VERSION=26.6.27
ARG XRAY_LINUX_64_SHA256=b3e5902d06d6282fe53cfa2fc426058b9aeaa429b2c812e20887cd47f26d08bf
ARG GIT_VERSION=dev

FROM alpine:${ALPINE_VERSION} AS downloader
ARG XRAY_VERSION
ARG XRAY_LINUX_64_SHA256
ARG APK_MIRROR
RUN sed -i "s#dl-cdn.alpinelinux.org#${APK_MIRROR}#g" /etc/apk/repositories \
    && apk add --no-cache curl unzip \
    && curl -fsSL "https://github.com/XTLS/Xray-core/releases/download/v${XRAY_VERSION}/Xray-linux-64.zip" -o /tmp/xray.zip \
    && echo "${XRAY_LINUX_64_SHA256}  /tmp/xray.zip" | sha256sum -c - \
    && unzip /tmp/xray.zip -d /tmp/xray \
    && chmod +x /tmp/xray/xray

FROM alpine:${ALPINE_VERSION}
ARG APK_MIRROR
RUN sed -i "s#dl-cdn.alpinelinux.org#${APK_MIRROR}#g" /etc/apk/repositories \
    && apk add --no-cache ca-certificates iproute2 su-exec \
    && addgroup -g 101 xraysocket \
    && adduser -D -H -u 1000 -G xraysocket appuser
COPY --from=downloader /tmp/xray/xray /usr/local/bin/xray
# geosite.dat 不复制:服务端模板(xray.json.tmpl)只用 geoip:private,geosite
# 引用全部在客户端配置(GenerateClientConfig,客户端自行下载资产)。若未来
# 服务端模板引用 geosite,需同步恢复 COPY。
COPY --from=downloader /tmp/xray/geoip.dat /usr/local/bin/geoip.dat
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

LABEL org.opencontainers.image.version="${XRAY_VERSION}" \
      org.opencontainers.image.revision="${GIT_VERSION}"
ENTRYPOINT ["/entrypoint.sh"]
