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

EXPOSE 8317

CMD ["cli-proxy-api", "run", "--config", "/CLIProxyAPI/config.yaml"]
