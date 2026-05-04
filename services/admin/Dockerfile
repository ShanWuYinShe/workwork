FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o admin .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates apache2-utils bash
COPY --from=builder /build/admin /app/admin

WORKDIR /app
ENTRYPOINT ["/app/admin"]
