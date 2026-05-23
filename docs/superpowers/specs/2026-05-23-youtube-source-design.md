# YouTube as a Second Audio Source — Design

**Date:** 2026-05-23
**Status:** Approved, ready for implementation plan
**Module:** `github.com/ZetoOfficial/spotify-download-tg-bot`
**Related:** [2026-05-18 base design](2026-05-18-spotify-download-tg-bot-design.md)

## Purpose

Расширить бота так, чтобы кроме ссылок на Spotify-треки он принимал
ссылки на YouTube-видео и отдавал их mp3 тем же способом
(транскод + ID3-теги + кэш + Telegram upload).

Личное использование, ≤50 юзеров, единичные треки. Hard cap на
длительность YouTube — 5 минут.

## Scope

**В скоупе:**

- Ссылки `https://(www.|m.)?youtube.com/watch?...v=<id>` и
  `https://youtu.be/<id>`.
- Hard cap 5 минут (300s). Длиннее — reply «максимум 5 минут».
- Метаданные для ID3: предпочтительно `artist`/`track` из yt-dlp;
  fallback — парсинг `title` по " - "; финальный fallback —
  `channel`/`uploader` как artist + `title` как title.
- Кэш переиспользуется (та же таблица, тот же on-disk LRU).
- Та же очередь, тот же per-user семафор.

**Вне скоупа:**

- `music.youtube.com`, `/shorts/`, `/playlist`, `/channel/`.
- Плейлисты на YouTube (yt-dlp флаг `--no-playlist` гарантирует один трек).
- Альтернативные качества/форматы для пользователя.
- Override 5-минутного лимита.
- Live-стримы, premieres (yt-dlp сам отфейлит → маппим в `ErrAudioNotFound`).

## Architecture

Ровно те же четыре шва (Resolver, Cache, AudioSource, Uploader).
Изменения:

1. Парсер ссылок возвращает «откуда» (Spotify/YouTube) + ID + URL.
2. `pipeline.Pipeline` хранит **два** Resolver'а (один на источник) и
   выбирает по `Job.Source`.
3. `audio.YtDlpSource.Fetch` ветвится по `Track.Source`: для YouTube
   качает по URL напрямую (без `ytsearch1:` и без duration sanity-check —
   он уже сделан в Resolver).
4. Cache использует `track_key` вместо `spotify_id`. Значения —
   `sp:<spotify_id>` или `yt:<video_id>`. Schema migration на старте.

Никаких новых шов-интерфейсов не вводим — переиспользуем существующие.

### Package changes

```
internal/bot/parser.go         — ParseLink → (Source, ID, URL)
internal/track/track.go        — +Source, +SourceURL
internal/metadata/spotify.go   — без изменений
internal/metadata/youtube.go   — НОВЫЙ: YoutubeResolver через yt-dlp
internal/audio/ytdlp.go        — внутренняя ветка по t.Source
internal/cache/schema.sql      — rename spotify_id → track_key
internal/cache/queries.sql     — переименование параметров
internal/cache/cache.go        — параметр trackKey, +миграция на старте
internal/pipeline/pipeline.go  — два резолвера, диспатч по Job.Source
internal/queue/queue.go        — +Source в Job
internal/bot/handler.go        — общий путь для двух источников
cmd/bot/main.go                — wiring обоих резолверов
```

## Components

### 1. URL parser (`internal/bot/parser.go`)

Тип `Source` живёт в отдельном пакете `internal/source` (см. §2);
парсер его импортирует.

```go
import "github.com/ZetoOfficial/spotify-download-tg-bot/internal/source"

type ParsedLink struct {
    Source source.Source
    ID     string
    URL    string // канонический URL для логов и для yt-dlp (YouTube)
}

func ParseLink(s string) (ParsedLink, error)
```

Regex:

- Spotify (как было): `(?:open\.spotify\.com(?:/intl-[a-z]{2})?/track/|spotify:track:)([A-Za-z0-9]{22})`
- YouTube: `(?:youtube\.com/watch\?(?:[^\s&]+&)*v=|youtu\.be/)([A-Za-z0-9_-]{11})`

Порядок: пробуем сначала Spotify, потом YouTube. Не нашли — `ErrInvalidURL`.

Для YouTube `URL` нормализуем к `https://www.youtube.com/watch?v=<id>` (стабильный формат для yt-dlp и логов).

Старая функция `ExtractSpotifyTrackID` удаляется — всё через `ParseLink`.

### 2. Track DTO (`internal/track/track.go`)

```go
type Track struct {
    Source     Source // "spotify" | "youtube"  (тип из internal/bot)
    SourceID   string // spotify id или youtube video id
    SourceURL  string // канонический URL источника
    Artist     string
    Title      string
    Album      string
    ISRC       string // только Spotify
    DurationMs int
    CoverURL   string
}
```

Поле `SpotifyID` удаляется (мигрируется на `SourceID`). Чтобы не было
циклического импорта `track → bot`, тип `Source` живёт в **отдельном
маленьком пакете** `internal/source` (новый, на 5 строк); и `bot`, и
`track` импортируют его.

```go
// internal/source/source.go
package source

type Source string
const (
    Spotify Source = "spotify"
    YouTube Source = "youtube"
)
```

### 3. YouTube Resolver (`internal/metadata/youtube.go`)

```go
type YoutubeResolver struct {
    binary string
    exec   func(ctx, args) (stdout, stderr []byte, err error)
}

func (r *YoutubeResolver) Resolve(ctx context.Context, videoURL string) (track.Track, error)
```

Команда: `yt-dlp --skip-download --dump-json --no-playlist <url>`.

Парсим:

```go
var p struct {
    ID         string `json:"id"`
    Title      string `json:"title"`
    Artist     string `json:"artist"`
    Track      string `json:"track"`
    Album      string `json:"album"`
    Channel    string `json:"channel"`
    Uploader   string `json:"uploader"`
    Duration   float64 `json:"duration"`
    Thumbnail  string `json:"thumbnail"`
    IsLive     bool   `json:"is_live"`
    LiveStatus string `json:"live_status"`
}
```

Cap: `int(math.Round(p.Duration)) > 300` → `ErrTrackTooLong`.
Live/premiere: `p.IsLive || p.LiveStatus == "is_upcoming"` → `ErrAudioNotFound`.

Маппинг полей:

- **Artist**: `p.Artist` if non-empty; else parse `p.Title` left of первой " - " (trim spaces); else `p.Channel` if non-empty; else `p.Uploader`.
- **Title**: `p.Track` if non-empty; else parse `p.Title` right of первой " - " (если split дал ≥2 части); else `p.Title` целиком.
- **Album**: `p.Album` (может быть пустым — это ок).
- **CoverURL**: `p.Thumbnail` (yt-dlp подбирает наилучший по умолчанию).
- **ISRC**: пусто.
- **DurationMs**: `int(p.Duration * 1000)`.
- **SourceID/Source/SourceURL**: проставляются вызывающим (pipeline), потому что Resolver их уже знает из аргумента, но удобнее проставить в одном месте.

Сигнатура `Resolver.Resolve(ctx, key string)`:
- для `SpotifyResolver` `key` = spotify ID
- для `YoutubeResolver` `key` = canonical YouTube URL

Pipeline сам проставит `Source`, `SourceID`, `SourceURL` в Track после Resolve.

### 4. AudioSource (`internal/audio/ytdlp.go`)

`YtDlpSource.Fetch` сейчас всегда ищет через `ytsearch1:`. Добавляем
ветку:

```go
func (s *YtDlpSource) Fetch(ctx context.Context, t track.Track) (string, error) {
    if t.Source == source.YouTube {
        return s.fetchByURL(ctx, t)
    }
    return s.fetchBySearch(ctx, t) // существующая логика, переименованная
}
```

`fetchByURL`:

- `outTpl = <workDir>/<SourceID>.%(ext)s` (без `sp:`/`yt:` префикса в имени файла — `SourceID` уже чистый).
- args: `yt-dlp --no-playlist -f "bestaudio[acodec!=opus]/bestaudio" --print-json --no-progress -o <outTpl> <t.SourceURL>`.
- Парсим stdout → определяем `ext`.
- **Duration sanity-check не делаем** — длительность уже проверена в Resolver, и `t.DurationMs` пришёл из того же источника, что и сам файл.
- Если yt-dlp вернул ошибку «video unavailable», «private video», «members-only» — маппим в `ErrAudioNotFound` (по шаблону в stderr).

### 5. Pipeline (`internal/pipeline/pipeline.go`)

```go
type Pipeline struct {
    Resolvers  map[source.Source]metadata.Resolver
    Cache      cache.Cache
    Audio      audio.Source
    Transcoder Transcoder
    Uploader   uploader.Uploader
    Notifier   Notifier
    CacheDir   string
    Logger     *slog.Logger
}
```

В `Process(ctx, j Job)`:

1. `r, ok := p.Resolvers[j.Source]` — если нет → лог ERROR + reply «сервис недоступен» (программная ошибка, не должна случаться при правильном wiring; assert через тест).
2. `key := resolverKey(j)` — для Spotify это `j.SourceID`, для YouTube — `j.SourceURL`.
3. `tr, err := r.Resolve(ctx, key)`. Маппинг ошибок:
   - `ErrTrackTooLong` → reply «максимум 5 минут».
   - `ErrSpotifyNotFound` / `ErrSpotifyAuth` — как раньше.
   - default — «не получилось обработать ссылку».
4. Pipeline проставляет `tr.Source = j.Source`, `tr.SourceID = j.SourceID`, `tr.SourceURL = j.SourceURL`.
5. Cache key: `trackKey := cache.Key(j.Source, j.SourceID)` (см. §6).
6. Дальше — без изменений: lookup → audio.Fetch → transcode → upload → save.

### 6. Cache (`internal/cache/*`)

**Schema (`schema.sql`):**

```sql
CREATE TABLE IF NOT EXISTS tracks (
  track_key    TEXT PRIMARY KEY,  -- 'sp:<id>' | 'yt:<id>'
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

**Queries (`queries.sql`):** все имена параметров переименовываются
`spotify_id` → `track_key`. sqlc-сгенерированные методы:
`GetTrack(ctx, trackKey)`, `UpsertTrack`, `TouchLastUsed`, `ClearLocalPath`.

**Cache.Key helper:**

```go
package cache

func Key(src source.Source, id string) string {
    switch src {
    case source.YouTube:
        return "yt:" + id
    default:
        return "sp:" + id
    }
}
```

**Cache interface** меняет имя параметра, оставаясь типизированно-плоским:

```go
type Cache interface {
    Lookup(ctx context.Context, trackKey string) (Entry, bool, error)
    Save(ctx context.Context, trackKey string, e Entry,
        artist, title, album string, durationMs int) error
    Touch(ctx context.Context, trackKey string) error
}
```

**Миграция на старте (`cache.go`, в `NewSQLiteCache`):**

Запускается ДО embedded schema. Идемпотентна.

```go
func migrateRenameSpotifyID(ctx context.Context, db *sql.DB) error {
    // 1. PRAGMA table_info(tracks): есть ли колонка spotify_id?
    // 2. Если нет — return nil (либо новая база, либо уже мигрирована).
    // 3. ALTER TABLE tracks RENAME COLUMN spotify_id TO track_key.
    // 4. UPDATE tracks SET track_key = 'sp:' || track_key
    //    WHERE track_key NOT LIKE 'sp:%' AND track_key NOT LIKE 'yt:%'.
}
```

Обёрнуто в транзакцию. SQLite ≥3.25 поддерживает `RENAME COLUMN`
(modernc.org/sqlite ≥1.20 — да).

Тесты миграции: на in-memory DB предзаполненной старой схемой проверяем,
что после миграции значения стали `sp:...` и старые запросы новых
методов работают.

### 7. Queue Job (`internal/queue/queue.go`)

```go
type Job struct {
    ChatID            int64
    UserID            int64
    Source            source.Source
    SourceID          string
    SourceURL         string
    ReplyMessageID    int
    OriginalMessageID int
}
```

`SpotifyURL`/`SpotifyID` поля удаляются.

### 8. Bot handler (`internal/bot/handler.go`)

```go
link, err := ParseLink(text)
if err != nil { /* reply 'пришли ссылку Spotify или YouTube' */ }
// далее как сейчас, но в Job кладём link.Source/ID/URL
```

`/start` reply: «Пришли ссылку на трек Spotify или видео YouTube (до 5 минут) — отвечу mp3.»

Сообщение об ошибке парсинга: «пришли ссылку на трек Spotify или YouTube».

### 9. Wiring (`cmd/bot/main.go`)

```go
spotifyRes := metadata.NewSpotifyResolver(cfg.SpotifyID, cfg.SpotifySecret)
youtubeRes := metadata.NewYoutubeResolver("yt-dlp")
pipe := &pipeline.Pipeline{
    Resolvers: map[source.Source]metadata.Resolver{
        source.Spotify: spotifyRes,
        source.YouTube: youtubeRes,
    },
    // ... остальное как было
}
```

## Error handling

Дополнения к существующей таблице:

| Ошибка | Источник | Реакция | Reply |
|---|---|---|---|
| `metadata.ErrTrackTooLong` | youtube resolver | edit reply | «максимум 5 минут» |
| `audio.ErrAudioNotFound` (YouTube unavailable/private) | audio | edit reply | «не получилось скачать аудио» |
| `bot.ErrInvalidURL` | parser | send reply | «пришли ссылку на трек Spotify или YouTube» |

## Testing

**Unit:**

- `bot/parser`: добавить таблицу:
  - `https://youtu.be/dQw4w9WgXcQ` → YouTube.
  - `https://www.youtube.com/watch?v=dQw4w9WgXcQ` → YouTube.
  - `https://youtube.com/watch?si=abc&v=dQw4w9WgXcQ&t=10s` → YouTube (порядок параметров не важен).
  - `https://m.youtube.com/watch?v=dQw4w9WgXcQ` → YouTube.
  - `https://music.youtube.com/watch?v=dQw4w9WgXcQ` → NOT supported → ErrInvalidURL.
  - `https://youtube.com/shorts/abc` → NOT supported → ErrInvalidURL.
  - `https://youtube.com/playlist?list=...` → ErrInvalidURL.
  - Spotify-кейсы остаются.
- `metadata/youtube`: фейковый `exec`, проверки:
  - artist/track из JSON.
  - title `"Artist - Song"` → парсится.
  - title `"random text"` без " - " → channel как artist, title как title.
  - duration 250s → ok; 320s → ErrTrackTooLong; ровно 300s → ok; 301s → ErrTrackTooLong.
  - `is_live: true` → ErrAudioNotFound.
  - yt-dlp вернул non-zero exit → wrapped error.
- `audio/ytdlp`: ветка для YouTube — проверяем, что в args нет `ytsearch1:`, есть URL; что duration check не блокирует (передаём заведомо отличающийся DurationMs).
- `cache`: миграция на in-memory DB:
  - стартуем со старой схемой + 2 строки `4iV5W9...` → миграция → строки стали `sp:4iV5W9...`, новые запросы возвращают их.
  - стартуем с новой схемой → миграция no-op.
  - стартуем с пустой базой → миграция no-op.
- `pipeline`: добавить таблицу для двух источников + ErrTrackTooLong.

**Integration (build tag `integration`):**

- `metadata/youtube_integration`: реальный yt-dlp на короткий публичный клип, проверяем непустые поля.
- e2e обновляем, чтобы покрыть YouTube-ссылку.

## Configuration

Новых env vars не добавляем. yt-dlp бинарь — тот же, что для Spotify.

## Migration / rollout

- Один Docker-образ, один `docker compose up -d`.
- При первом старте новой версии — миграция таблицы tracks. На небольшом числе строк (десятки/сотни) — мгновенно.
- Откат версии: новая база со старой версией работать не будет (нет колонки spotify_id). При необходимости отката админ должен либо
  - оставить волюм нетронутым и rollback образ → база уже мигрирована, старая версия её не прочитает (упадёт на `GetTrack`); либо
  - снести `bot.db` и пересоздать (кэш потеряется, но трек-данные восстановятся при следующих запросах). Для личного бота приемлемо.

## Out-of-scope risks

- yt-dlp может найти/качать не то для конкретных видео (region lock, age gate) — маппим в `ErrAudioNotFound`.
- YouTube меняет регексп URL — обновляем парсер вручную.
- `is_live` / premiere — проверяем заранее, не качаем.
- Не валидируем, что трек реально музыкальный (мог бы быть подкаст/лекция). До 5 минут — личная ответственность.
