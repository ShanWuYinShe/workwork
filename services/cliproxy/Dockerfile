FROM alpine:latest

RUN apk add --no-cache curl unzip bash

ARG VERSION=6.10.0
RUN curl -fsSL https://github.com/router-for-me/CLIProxyAPI/releases/download/v${VERSION}/CLIProxyAPI_${VERSION}_linux_amd64.tar.gz -o /tmp/cliproxyapi.tar.gz && \
    tar -xzf /tmp/cliproxyapi.tar.gz -C /usr/local/bin && \
    rm /tmp/cliproxyapi.tar.gz && \
    chmod +x /usr/local/bin/cli-proxy-api 2>/dev/null || true

WORKDIR /CLIProxyAPI

EXPOSE 8317

CMD ["cli-proxy-api", "run", "--config", "/CLIProxyAPI/config.yaml"]
