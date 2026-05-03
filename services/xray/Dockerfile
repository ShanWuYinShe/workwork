ARG XRAY_IMAGE

FROM scratch AS base
COPY /entrypoint.sh /entrypoint.sh
COPY /xray /usr/local/bin/xray

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=base /entrypoint.sh /entrypoint.sh
COPY --from=base /usr/local/bin/xray /usr/local/bin/xray
RUN chmod +x /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
