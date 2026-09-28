# syntax=docker/dockerfile:1.7

FROM golang:1.26.3-alpine AS builder
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bot ./cmd/bot

FROM alpine:3.22
RUN apk add --no-cache ffmpeg python3 py3-pip nodejs ca-certificates
# YouTube needs a JS runtime for signature/n-challenge solving; yt-dlp only
# enables deno by default, so point it at node (EJS scripts ship via [default]).
RUN printf -- '--js-runtimes node\n' > /etc/yt-dlp.conf
# Bumped by CI on every build so the pip layer is never served from cache:
# a stale yt-dlp gets 403s from YouTube within weeks.
ARG YTDLP_CACHEBUST=0
RUN echo "yt-dlp cachebust: ${YTDLP_CACHEBUST}" \
 && pip3 install --break-system-packages --no-cache-dir -U "yt-dlp[default]"
WORKDIR /app
COPY --from=builder /out/bot /app/bot
VOLUME ["/app/cache", "/app/data"]
ENV CACHE_DIR=/app/cache SQLITE_PATH=/app/data/bot.db
ENTRYPOINT ["/app/bot"]
