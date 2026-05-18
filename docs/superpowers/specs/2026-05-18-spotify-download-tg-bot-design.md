# Spotify Download Telegram Bot — Design

**Date:** 2026-05-18
**Status:** Approved, ready for implementation plan
**Module:** `github.com/ZetoOfficial/spotify-download-tg-bot`
**Language/runtime:** Go 1.25.5

## Purpose

Telegram-бот для личного/дружеского использования (≤50 пользователей).
Пользователь шлёт ссылку на трек Spotify — бот отвечает mp3-файлом со
встроенными ID3-тегами и обложкой.

## Scope

**В скоупе:**

- Один трек по ссылке (`open.spotify.com/track/<id>` или
  `spotify:track:<id>`, с/без `?si=...`).
- Скачивание аудио через yt-dlp с YouTube/YouTube Music.
- Транскод в mp3 320 kbps + ID3v2 (Title, Artist, Album) + встроенная
  обложка.
- Кэш на двух уровнях: Telegram `file_id` (мгновенный resend) и
  локальный mp3 на диске (для переотдачи, если file_id протух).
- SQLite через sqlc.
- Один Docker-контейнер, long-polling, in-memory worker pool.

**Вне скоупа (v1):**

- Плейлисты, альбомы, артисты.
- Поиск по тексту сообщения.
- Deezer/SoundCloud источники (но архитектурный шов оставляем).
- Публичный доступ, rate limiting, биллинг, аналитика.
- Распределённые очереди, S3, k8s.

## Architecture

Один Go-бинарь, один процесс. Long-polling к Telegram, in-memory
channel-based очередь, N=2 воркера. SQLite-файл и папка `cache/` рядом
с бинарём; в Docker — на volume.

### Package layout

```
cmd/bot/main.go              — wiring: env, конструируем реализации,
                                запускаем bot+воркеры, graceful shutdown
internal/bot/                — telegram handlers, парсинг URL, ответы
internal/pipeline/           — оркестратор: Track-job → 4 шва
internal/metadata/           — MetadataResolver (Spotify Client Credentials)
internal/audio/              — AudioSource (yt-dlp shell-out)
internal/transcode/          — ffmpeg shell-out: raw → mp3 320 + ID3 + cover
internal/cache/              — Cache (sqlc-generated queries + filesystem)
internal/cache/db/           — sqlc-generated (НЕ редактировать)
internal/uploader/           — Uploader (Telegram sendAudio → file_id)
internal/queue/              — buffered channel + worker pool
internal/track/              — общий DTO Track
```

### Interfaces (швы)

Четыре интерфейса на естественных границах замены технологий. Каждый —
1-2 метода, не больше.

```go
// internal/metadata
type Resolver interface {
    Resolve(ctx context.Context, spotifyURL string) (track.Track, error)
}

// internal/audio
type Source interface {
    Fetch(ctx context.Context, t track.Track) (rawAudioPath string, err error)
}

// internal/cache
type Cache interface {
    Lookup(ctx context.Context, spotifyID string) (Entry, bool, error)
    Save(ctx context.Context, spotifyID string, e Entry) error
    Touch(ctx context.Context, spotifyID string) error
}

// internal/uploader
type Uploader interface {
    Upload(ctx context.Context, chatID int64, mp3Path string, t track.Track) (fileID string, err error)
    SendCached(ctx context.Context, chatID int64, fileID string) error
}
```

### DTOs

```go
// internal/track
type Track struct {
    SpotifyID  string
    Artist     string
    Title      string
    Album      string
    ISRC       string
    DurationMs int
    CoverURL   string
}

// internal/cache
type Entry struct {
    FileID    string    // может быть пустым
    LocalPath string    // может быть пустым
    ExpiresAt time.Time // для документации, реальная политика — LRU
}
```

### Что НЕ интерфейс

- `transcode` — всегда ffmpeg, шов не нужен.
- `queue` — простой channel + воркеры, не нужно подменять.
- `bot` — handler-функции, имеют прямую зависимость от Telegram lib.

## Data flow

### Handler (синхронно, ms)

1. Получили `message.Text`. Регексп: `(?:open\.spotify\.com/track/|spotify:track:)([A-Za-z0-9]+)`.
   Не нашли — reply: «пришли ссылку на трек Spotify». Выход.
2. `Cache.Lookup(spotifyID)`.
   Если `Entry.FileID != ""` → `Uploader.SendCached(chatID, fileID)`,
   `Cache.Touch(spotifyID)`. Готово.
3. Пытаемся захватить per-user семафор (неблокирующий). Занят → reply
   «жди, твой прошлый трек ещё качается», выход.
4. Кладём job `{ChatID, SpotifyID, ReplyMessageID}` в очередь.
   Отвечаем reply’ем «⏳ качаю…», сохраняем `messageID` ответа в job
   для последующего edit/delete. Освобождение семафора — в `defer`
   воркера после обработки job (успех или ошибка — всё равно).

### Worker (асинхронно, секунды)

1. `MetadataResolver.Resolve(spotifyURL)` → `Track`.
   Ошибка → edit reply мапится по таблице ошибок (см. ниже).
2. `Cache.Lookup` повторно (race: трек уже скачался в параллельной
   job).
   - Hit с `FileID` → `SendCached`, edit «готово». Done.
   - Hit с `LocalPath` без `FileID` → `Upload(localPath)`, обновляем
     `file_id` через `Cache.Save`. Done.
3. `AudioSource.Fetch(track)` → путь к raw-аудио (m4a/opus).
   Стратегия yt-dlp:
   - попытка 1: query `"<artist> - <title>"`, search top-1
   - попытка 2: query `"<artist> <title> official audio"`
   - sanity-check: `|fetched.duration - track.DurationMs| < 5000`
   Обе не сходятся → `ErrAudioNotFound`.
4. `transcode.ToMP3(raw, track)` → `./cache/<spotifyID>.mp3` с ID3v2:
   - TIT2 = Title, TPE1 = Artist, TALB = Album
   - APIC = байты `track.CoverURL` (скачиваем HTTP GET; ошибка — не
     фейлим, просто без обложки)
   - ffmpeg flags: `-vn -c:a libmp3lame -b:a 320k -id3v2_version 3`
5. `Uploader.Upload(chatID, mp3Path, track)` → отправляет `sendAudio`
   с `performer/title/duration`, парсит ответ, возвращает `file_id`.
6. `Cache.Save(spotifyID, Entry{FileID, LocalPath: mp3Path,
   ExpiresAt: now+30d})`.
7. Чистим временные файлы (raw). `mp3Path` остаётся в `cache/`.
8. Edit/delete сообщения «⏳ качаю…».

### Concurrency

- Очередь: `chan Job` буфер 64.
- Воркеры: N=2 (env `WORKERS`, дефолт 2). yt-dlp+ffmpeg CPU/IO-heavy.
- Per-user семафор: `map[int64]chan struct{}` capacity 1, чтобы один
  юзер не забил всю очередь. Захват — неблокирующий, занято → reply
  «жди, твой прошлый трек ещё качается».
- Graceful shutdown: SIGTERM → закрываем приём из Telegram, ждём
  drain очереди до 30s, потом cancel.

## Storage (sqlc + SQLite)

### Driver

`modernc.org/sqlite` — pure-Go, без CGO. У него `database/sql`-совместимый
driver, sqlc-сгенерированный код работает через `*sql.DB`.

### Schema (`internal/cache/schema.sql`)

```sql
CREATE TABLE IF NOT EXISTS tracks (
  spotify_id   TEXT PRIMARY KEY,
  artist       TEXT NOT NULL,
  title        TEXT NOT NULL,
  album        TEXT NOT NULL DEFAULT '',
  duration_ms  INTEGER NOT NULL,
  file_id      TEXT,
  local_path   TEXT,
  created_at   INTEGER NOT NULL,
  last_used_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tracks_last_used ON tracks(last_used_at);
```

Файл эмбедится через `//go:embed` и прогоняется на старте бота
(идемпотентный `CREATE TABLE IF NOT EXISTS`). Миграционный тул в v1
не берём; если позже понадобится — добавим goose без переписывания.

### Queries (`internal/cache/queries.sql`)

- `GetTrack` (by spotify_id) — `:one`
- `UpsertTrack` (INSERT … ON CONFLICT DO UPDATE; устанавливает
  `last_used_at = ?` в обеих ветках, поэтому отдельный Touch после
  Save не нужен) — `:exec`
- `TouchLastUsed` (set last_used_at = ? WHERE spotify_id = ?;
  используется только на cache-hit без Save) — `:exec`
- `ListLRUCandidates` (WHERE local_path IS NOT NULL ORDER BY
  last_used_at ASC LIMIT ?) — `:many`
- `ClearLocalPath` (set local_path = NULL WHERE spotify_id = ?) — `:exec`

### sqlc.yaml (в корне)

```yaml
version: "2"
sql:
  - engine: sqlite
    queries: internal/cache/queries.sql
    schema:  internal/cache/schema.sql
    gen:
      go:
        package: db
        out: internal/cache/db
        sql_package: database/sql
        emit_json_tags: false
        emit_pointers_for_null_types: true
```

### Cache.Lookup правила

- `file_id IS NOT NULL` → hit; зовущий использует `SendCached`.
- `file_id IS NULL AND local_path IS NOT NULL AND file_exists` →
  частичный hit; зовущий делает `Upload`, потом `Save` с file_id.
- Иначе miss.
- На любой hit зовущий вызывает `Cache.Touch`.

### LRU/TTL на диске

При каждом `Save`: считаем размер `cache/`. Если >`MAX_CACHE_MB`
(env, дефолт 2048) — `ListLRUCandidates` берёт самые старые,
удаляем файлы, `ClearLocalPath` обнуляет `local_path`. `file_id`
оставляем (живёт у Telegram долго). Без отдельного крона — пиггибэк
на запись.

## Error handling

Каждый внешний вызов возвращает типизированную ошибку (`var Err… = errors.New(…)`
или sentinel). Pipeline ловит, маппит на user-facing reply, логирует
структурно через `log/slog` (JSON в stdout).

| Ошибка | Источник | Реакция | Reply пользователю |
|---|---|---|---|
| `ErrInvalidURL` | bot/parser | не идём в очередь | «пришли ссылку на трек Spotify» |
| `ErrSpotifyNotFound` | metadata (404) | edit reply | «трек не найден в Spotify» |
| `ErrSpotifyAuth` | metadata (401/403) | edit reply, log ERROR | «сервис недоступен, напиши админу» |
| `ErrAudioNotFound` | audio (обе попытки) | edit reply | «не получилось скачать аудио» |
| `ErrTranscode` | transcode | edit reply, log ERROR + stderr | «ошибка конвертации» |
| `ErrUpload` (Telegram 5xx) | uploader | retry 2× с backoff (1s, 4s), потом fail | «telegram отвалился, попробуй ещё раз» |
| `ctx.Cancel` (shutdown) | любой | job теряется (допустимо) | — |

**Spotify token:** Client Credentials, кешируется в памяти, обновляется
за 60s до истечения часового TTL.

**Логи:** `slog` JSON, поля `spotify_id`, `chat_id`, `stage`,
`duration_ms`, `err`. В логи не пишем username/полный текст сообщений.

## Testing

### Unit (быстро, без сети, без бинарей yt-dlp/ffmpeg)

- `bot/parser`: табличные тесты на варианты URL и мусор.
- `metadata`: фейковый `http.RoundTripper`, проверяем парсинг,
  обработку 401/404, token refresh.
- `cache`: `:memory:` SQLite через настоящий драйвер + sqlc-код,
  проверяем Lookup/Save/Touch/LRU-эвикцию, race на параллельную
  запись.
- `pipeline`: мокаем все 4 интерфейса (in-package fakes), проверяем
  порядок вызовов, ветки cache-hit/miss/partial, маппинг ошибок.

### Integration (медленнее, build tag `integration`)

- `audio`: реальный yt-dlp на короткий публичный YouTube-клип,
  проверяем существование файла и duration.
- `transcode`: реальный ffmpeg на маленький wav-fixture, проверяем
  mp3 на выходе через `ffprobe`.
- e2e smoke: реальный бот-токен из env, шлём заранее заготовленную
  ссылку в тестовый чат, читаем ответ. Запускается локально/вручную,
  не в CI.

### CI (GitHub Actions)

- `go test ./...` (unit, всегда)
- `golangci-lint run`
- `sqlc generate` + `git diff --exit-code internal/cache/db` —
  страховка от рассинхрона генерации
- integration job — отдельный, `workflow_dispatch`, с установкой
  yt-dlp/ffmpeg в раннер

### Целевое покрытие

~70% на `pipeline` и `cache` (вся логика там); `metadata` и
`bot/parser` — по факту. Остальное best-effort.

## Configuration (env)

| Var | Default | Описание |
|---|---|---|
| `TELEGRAM_BOT_TOKEN` | — (required) | токен бота |
| `SPOTIFY_CLIENT_ID` | — (required) | Spotify API |
| `SPOTIFY_CLIENT_SECRET` | — (required) | Spotify API |
| `WORKERS` | `2` | размер воркер-пула |
| `QUEUE_SIZE` | `64` | буфер очереди |
| `CACHE_DIR` | `./cache` | папка mp3 |
| `MAX_CACHE_MB` | `2048` | лимит размера cache до LRU-эвикции |
| `SQLITE_PATH` | `./bot.db` | путь к SQLite |
| `ALLOWED_USER_IDS` | пусто = все | comma-separated whitelist (опц.) |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |

## Deployment

Один Dockerfile (multi-stage):

1. builder: `golang:1.25` → `go build -o bot ./cmd/bot`
2. runtime: `alpine` + `yt-dlp` (через `pip install --no-cache-dir`)
   + `ffmpeg` (apk) + статически собранный бинарь.

`docker-compose.yml`: один сервис, volume для `./cache` и `./bot.db`,
env-файл, restart policy `unless-stopped`.

CI/CD — отдельный шаг, в этот спек не входит (предполагается ручной
`docker compose pull && up -d` или подключение skill go-cicd-deploy
позже).

## External dependencies

- Go libs: `github.com/go-telegram/bot` (или эквивалент), sqlc-generated
  через `database/sql`, `modernc.org/sqlite`, `log/slog` (стд).
  Конкретный telegram lib фиксируется в плане имплементации.
- CLI tools (в Docker-образе): `yt-dlp`, `ffmpeg`.
- External APIs: Spotify Web API (Client Credentials), Telegram Bot API.

## Out-of-scope risks (документируем, не решаем)

- yt-dlp может найти «не тот» трек — sanity-check по duration частично
  это ловит, но не на 100%. Для личного бота приемлемо.
- Spotify меняет URL-формат — регексп придётся обновить.
- Telegram file_id протухает (теоретически возможно) — есть fallback
  через `local_path`.
- yt-dlp ломается при обновлениях YouTube — пин-версия в Docker,
  обновляем вручную.
