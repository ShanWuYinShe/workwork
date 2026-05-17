ARG XRAY_VERSION=26.5.3

FROM alpine:3.20 AS downloader
ARG XRAY_VERSION
RUN apk add --no-cache curl unzip \
    && curl -fsSL "https://github.com/XTLS/Xray-core/releases/download/v${XRAY_VERSION}/Xray-linux-64.zip" -o /tmp/xray.zip \
    && unzip /tmp/xray.zip -d /tmp/xray \
    && chmod +x /tmp/xray/xray

FROM alpine:3.20
RUN apk add --no-cache ca-certificates iproute2
COPY --from=downloader /tmp/xray/xray /usr/local/bin/xray
COPY --from=downloader /tmp/xray/geosite.dat /usr/local/bin/geosite.dat
COPY --from=downloader /tmp/xray/geoip.dat /usr/local/bin/geoip.dat
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
