FROM alpine:3.20 AS builder

RUN apk add --no-cache curl unzip

ARG VERSION=6.10.0
RUN curl -fsSL https://github.com/router-for-me/CLIProxyAPI/releases/download/v${VERSION}/CLIProxyAPI_${VERSION}_linux_amd64.tar.gz -o /tmp/cliproxyapi.tar.gz && \
    tar -xzf /tmp/cliproxyapi.tar.gz -C /tmp && \
    rm /tmp/cliproxyapi.tar.gz

FROM alpine:3.20

RUN apk add --no-cache ca-certificates wget

COPY --from=builder /tmp/cli-proxy-api /usr/local/bin/cli-proxy-api

WORKDIR /CLIProxyAPI

# 不使用 USER appuser：同 admin Dockerfile 原因，uid 不匹配服务器用户。
# bind mount 覆盖 /root/.cli-proxy-api 目录，Dockerfile 内的 chown 无实际意义。
# 安全保护由 docker-compose 中 cap_drop:ALL + no-new-privileges 实现。

EXPOSE 8317

CMD ["cli-proxy-api", "run", "--config", "/CLIProxyAPI/config.yaml"]
