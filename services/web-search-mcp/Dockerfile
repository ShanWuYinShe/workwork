FROM golang:1.26.2-alpine AS build
WORKDIR /app
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /app/web-search-mcp .

FROM alpine:3.22.4
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /app/web-search-mcp .
EXPOSE 8320
USER nobody
CMD ["./web-search-mcp"]
