FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git

WORKDIR /build

COPY go.mod ./
RUN go mod download github.com/coreos/go-oidc/v3 github.com/docker/docker

COPY . .
RUN go mod tidy
RUN CGO_ENABLED=0 go build -o admin .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates
COPY --from=builder /build/admin /app/admin

WORKDIR /app
EXPOSE 8318

ENTRYPOINT ["/app/admin"]
