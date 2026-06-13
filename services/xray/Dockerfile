ARG ALPINE_VERSION=3.22.4
ARG XRAY_VERSION=26.5.3
ARG XRAY_LINUX_64_SHA256=f9d7dd7492791430d48a9828fb36f32663577af787e8b363a1548dece21a7897

FROM alpine:${ALPINE_VERSION} AS downloader
ARG XRAY_VERSION
ARG XRAY_LINUX_64_SHA256
RUN apk add --no-cache curl unzip \
    && curl -fsSL "https://github.com/XTLS/Xray-core/releases/download/v${XRAY_VERSION}/Xray-linux-64.zip" -o /tmp/xray.zip \
    && echo "${XRAY_LINUX_64_SHA256}  /tmp/xray.zip" | sha256sum -c - \
    && unzip /tmp/xray.zip -d /tmp/xray \
    && chmod +x /tmp/xray/xray

FROM alpine:${ALPINE_VERSION}
RUN apk add --no-cache ca-certificates iproute2 su-exec \
    && addgroup -g 101 xraysocket \
    && adduser -D -H -u 1000 -G xraysocket appuser
COPY --from=downloader /tmp/xray/xray /usr/local/bin/xray
COPY --from=downloader /tmp/xray/geosite.dat /usr/local/bin/geosite.dat
COPY --from=downloader /tmp/xray/geoip.dat /usr/local/bin/geoip.dat
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
