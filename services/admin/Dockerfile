FROM node:22-alpine AS ui-builder
WORKDIR /ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build

FROM golang:1.26-alpine AS go-builder
RUN apk add --no-cache git
WORKDIR /build
COPY services/admin/go.mod services/admin/go.sum ./
RUN go mod download
COPY services/admin/ ./
RUN CGO_ENABLED=0 go build -o admin .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates apache2-utils bash
COPY --from=go-builder /build/admin /app/admin
COPY --from=ui-builder /ui/dist /app/ui
WORKDIR /app
ENTRYPOINT ["/app/admin"]
