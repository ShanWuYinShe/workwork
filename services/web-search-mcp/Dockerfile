FROM golang:1.26.5-alpine AS build
WORKDIR /app
ARG GOPROXY
ENV GOPROXY=${GOPROXY:-https://goproxy.cn,direct}
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /app/web-search-mcp .

FROM alpine:3.24.1
RUN sed -i 's#dl-cdn.alpinelinux.org#mirrors.aliyun.com#g' /etc/apk/repositories \
    && apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /app/web-search-mcp .
EXPOSE 8320
USER nobody
CMD ["./web-search-mcp"]
