ARG ALPINE_VERSION=3.24.1
# 镜像源可覆盖(默认 aliyun,国内网络现实;换环境 build-arg 覆盖)。
ARG APK_MIRROR=mirrors.aliyun.com
# 26.11.9（2026-11-09）经评估升级：区间含两个紧急安全修复——MPTCP 默认关闭
# （Linux 内核 IPv4 MPTCP 远程 root，Go 1.24+ 默认开启 MPTCP）与内置 RootCAs
# 移除已被破解的三张中间证书；无配置破坏性变更，服务端仅承载 XHTTP。
# 版本升级仍另行评估，不随无关变更顺带升。
ARG XRAY_VERSION=26.11.9
ARG XRAY_LINUX_64_SHA256=b651f7e1d9966ee2c1b1ee1bf8b52c72771824d3065dce96bbc8bcc19ed95f82
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
# 必须在本 stage 重新声明：FROM 之前声明的 ARG 不会传入 stage，LABEL 会
# 渲染成空串（实测 version/revision 均为空）。
ARG APK_MIRROR
ARG XRAY_VERSION
ARG GIT_VERSION
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
