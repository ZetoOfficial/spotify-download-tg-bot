# syntax=docker/dockerfile:1.7

FROM golang:1.26.3-alpine AS builder
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bot ./cmd/bot

FROM alpine:3.20
RUN apk add --no-cache ffmpeg python3 py3-pip ca-certificates \
 && pip3 install --break-system-packages --no-cache-dir yt-dlp
WORKDIR /app
COPY --from=builder /out/bot /app/bot
VOLUME ["/app/cache", "/app/data"]
ENV CACHE_DIR=/app/cache SQLITE_PATH=/app/data/bot.db
ENTRYPOINT ["/app/bot"]
