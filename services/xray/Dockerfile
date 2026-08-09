ARG ALPINE_VERSION=3.24.1
ARG XRAY_VERSION=26.7.28
ARG XRAY_LINUX_64_SHA256=8195d909f1109b8f3d99eefe401a3c451d7bf4af71f24d3815420f77e5dd2a40

FROM alpine:${ALPINE_VERSION} AS downloader
ARG XRAY_VERSION
ARG XRAY_LINUX_64_SHA256
RUN sed -i 's#dl-cdn.alpinelinux.org#mirrors.aliyun.com#g' /etc/apk/repositories \
    && apk add --no-cache curl unzip \
    && curl -fsSL "https://github.com/XTLS/Xray-core/releases/download/v${XRAY_VERSION}/Xray-linux-64.zip" -o /tmp/xray.zip \
    && echo "${XRAY_LINUX_64_SHA256}  /tmp/xray.zip" | sha256sum -c - \
    && unzip /tmp/xray.zip -d /tmp/xray \
    && chmod +x /tmp/xray/xray

FROM alpine:${ALPINE_VERSION}
RUN sed -i 's#dl-cdn.alpinelinux.org#mirrors.aliyun.com#g' /etc/apk/repositories \
    && apk add --no-cache ca-certificates iproute2 su-exec \
    && addgroup -g 101 xraysocket \
    && adduser -D -H -u 1000 -G xraysocket appuser
COPY --from=downloader /tmp/xray/xray /usr/local/bin/xray
COPY --from=downloader /tmp/xray/geosite.dat /usr/local/bin/geosite.dat
COPY --from=downloader /tmp/xray/geoip.dat /usr/local/bin/geoip.dat
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
