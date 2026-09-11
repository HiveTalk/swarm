# Architecture

This document describes the high-level architecture of **Swarm**, a team-based
Nostr relay with an optional Blossom media server, an admin dashboard, and a
scheduled-post service. Swarm is a fork of
[bitvora/team-relay](https://github.com/bitvora/team-relay), written in Go and
built on top of the [Khatru](https://khatru.nostr.technology/) relay framework.

## Overview

Swarm is a single-process Go application that speaks the
[Nostr](https://github.com/nostr-protocol/nips) protocol over WebSockets and
exposes a number of HTTP endpoints alongside it. It enforces team-based access
control, rate limiting, and spam protection, and can optionally act as a
[Blossom](https://github.com/hzrd149/blossom) media server backed by either the
local filesystem or an S3-compatible object store.

```
                       ┌─────────────────────────────────────────────┐
                       │                  swarm binary                │
                       │                                             │
   WebSocket clients ──┤── Khatru Relay (NIP-01)                    │
   (nostr events)      │   ├── StoreEvent / QueryEvents / Delete   │
                       │   ├── RejectEvent (access control + RL)    │
                       │   ├── RejectConnection (IP RL)            │
                       │   └── RejectFilter (query RL)              │
                       │                                             │
   HTTP clients ───────┤── HTTP mux (relay.Router())                │
   (dashboard, blossom,│   ├── /            front page              │
   scheduler, media)   │   ├── /dashboard   admin UI                │
                       │   ├── /api/dashboard|/api/admin  admin API  │
                       │   ├── /api/scheduler/*  scheduler API       │
                       │   ├── /api/health       health check        │
                       │   ├── /public/*         static assets      │
                       │   ├── /convert         NIP-05 converter   │
                       │   ├── /list/<pubkey>   blossom list        │
                       │   ├── /mirror          blossom mirror     │
                       │   └── /<sha256>...     blossom blob I/O    │
                       └─────────────────────────────────────────────┘
                          │           │              │            │
                          ▼           ▼              ▼            ▼
                   Event Store    nostr.json    Blossom Store   Scheduler
                   (postgres /    (team roster, (filesystem /   (JSON file
                    badger /       in-memory     S3)             on disk)
                    lmdb)          cache)
```

## Source layout

Swarm is a single Go package (`package main`) split across a few files for
readability. There is no internal module boundary; all files live at the
repository root.

| File                | Responsibility                                                                 |
|---------------------|--------------------------------------------------------------------------------|
| `main.go`           | Entry point, configuration loading, relay wiring, access control, dashboard & Blossom HTTP handlers, NIP-05 helpers. |
| `frontend.go`       | In-process HTML template and handler for the public front page (`/`).         |
| `scheduler.go`      | Scheduled-post store, background publisher, and `/api/scheduler/*` HTTP API. |
| `s3storage.go`      | S3-compatible storage backend for Blossom blobs (Tigris, AWS S3, MinIO, …).    |
| `s3storage_test.go` | Tests for the S3 storage backend.                                              |
| `public/`           | Static assets served at `/public/*` (images, `dashboard.html`, `convert.html`, `js/NostrLogin.js`). |
| `templates/`        | Server-rendered HTML templates (`dashboard_view.html`).                       |
| `setup/`            | Operational scripts (`deploy.sh`, `upload.sh`, `blossom-upload.go`, nginx config). |
| `Dockerfile`, `docker-compose.yml`, `fly.toml`, `zeabur.yaml` | Container & deployment manifests. |
| `.env.example`, `ENV_VARIABLES.md`, `ENV_CONFIG_GUIDE.md` | Configuration reference. |

## Core components

### 1. Relay core (Khatru)

The relay is created with `khatru.NewRelay()` and configured in `main()`.
Khatru exposes a number of extension points that Swarm populates:

- **`StoreEvent` / `QueryEvents` / `DeleteEvent`** — wired to the chosen
  `DBBackend` implementation (see [Event storage](#2-event-storage)).
- **`RejectEvent`** — a chain of predicates evaluated for every incoming
  event. Swarm appends several:
  1. Trusted-client exception (bypass for configured `["client","<name>"]` tags).
  2. Delete (kind 5) policy: team members can delete anything; public users
     can delete their own posts when `PUBLIC_ALLOWED_KINDS` is configured.
  3. Public-kind allowlist (`PUBLIC_ALLOWED_KINDS`).
  4. Team membership check against `nostr.json`.
  5. Team-kind allowlist (`ALLOWED_KINDS`).
  6. Pubkey rate limit (non-team members).
  7. IP rate limit.
  8. Base64 media rejection (spam vector).
- **`RejectConnection`** — IP connection rate limit.
- **`RejectFilter`** — IP query rate limit.

The relay's HTTP router (`relay.Router()`) is reused for all non-WebSocket
HTTP endpoints, so the relay and the HTTP API share a single `http.Server`
with extended timeouts tuned for large Blossom uploads.

### 2. Event storage

Storage is abstracted behind the `DBBackend` interface defined in `main.go`:

```go
type DBBackend interface {
    Init() error
    Close()
    CountEvents(ctx context.Context, filter nostr.Filter) (int64, error)
    DeleteEvent(ctx context.Context, evt *nostr.Event) error
    QueryEvents(ctx context.Context, filter nostr.Filter) (chan *nostr.Event, error)
    SaveEvent(ctx context.Context, evt *nostr.Event) error
    ReplaceEvent(ctx context.Context, evt *nostr.Event) error
}
```

`newDBBackend()` selects an implementation based on `DB_ENGINE`:

| `DB_ENGINE` | Implementation                                  | Notes                                   |
|-------------|-------------------------------------------------|-----------------------------------------|
| `postgres`  | `eventstore/postgresql.PostgresBackend`         | Default. Uses `DATABASE_URL` or individual `POSTGRES_*` vars. |
| `badger`    | `eventstore/badger.BadgerBackend`               | Embedded KV store. Used by `docker-compose.yml`. |
| `lmdb`      | `eventstore/lmdb.LMDBBackend`                   | Embedded KV store. Requires `liblmdb-dev` to build. |

All three backends come from the upstream
[`fiatjaf/eventstore`](https://github.com/fiatjaf/eventstore) library.

### 3. Team roster (`nostr.json`)

Team membership is driven by a NIP-05-style `nostr.json` document mapping
usernames to pubkeys. The special `_` entry is the relay operator / dashboard
admin.

`fetchNostrData()` loads the roster at startup and refreshes it hourly:

- If `NPUB_DOMAIN` is set, the roster is fetched over HTTPS from
  `https://<domain>/public/.well-known/nostr.json` (falling back to
  `/.well-known/nostr.json`).
- Otherwise the local `public/.well-known/nostr.json` file is read.

The in-memory `data` (`NostrData{Names, Relays}`) is consulted by the access
control predicates and the dashboard API. When the roster is local
(`NPUB_DOMAIN` unset), the dashboard API can add/update/delete users and write
the changes back to disk via `addOrUpdateUser` / `deleteUser`.

### 4. Access control model

Swarm implements a three-tier hierarchical access model, evaluated in the
`RejectEvent` chain:

1. **Trusted clients** (highest priority) — events carrying a
   `["client","<TRUSTED_CLIENT_NAME>"]` tag bypass normal restrictions for the
   configured kinds (or all kinds when `TRUSTED_CLIENT_KINDS="all"`).
2. **Public users** — any pubkey may post event kinds listed in
   `PUBLIC_ALLOWED_KINDS`. Public users can also delete their own posts
   (kind 5 with `e` tags).
3. **Team members** — pubkeys listed in `nostr.json`. They may post anything in
   `ALLOWED_KINDS` (or all kinds when `ALLOWED_KINDS` is empty), delete any
   event, and bypass pubkey rate limits.

### 5. Rate limiting & spam protection

`applySpamProtection()` instantiates four independent in-memory sliding-window
rate limiters (`rateLimiter` in `main.go`), each configurable via environment
variables (unset/empty disables the limiter):

| Limiter              | Env var              | Default window | Scope                  |
|----------------------|----------------------|----------------|------------------------|
| `pubkeyRateLimit`    | `PUBKEY_RATE_LIMIT`  | 1 minute       | events per pubkey (non-team) |
| `ipRateLimit`        | `IP_RATE_LIMIT`      | 1 minute       | events per IP          |
| `connRateLimit`      | `CONN_RATE_LIMIT`    | 2 minutes      | WebSocket connections per IP |
| `queryRateLimit`     | `QUERY_RATE_LIMIT`   | 1 minute       | filters/queries per IP |

Team members are exempt from the pubkey rate limit. A final `RejectEvent`
predicate rejects any event whose content contains `data:image/` or
`data:video/` (inline base64 media).

### 6. Blossom media server

When `BLOSSOM_ENABLED=true`, Swarm attaches a Blossom server
(`khatru/blossom`) to the relay. Blob metadata is indexed in the event store
via `blossom.EventStoreBlobIndexWrapper`; blob bytes are stored via pluggable
`StoreBlob` / `LoadBlob` / `DeleteBlob` callbacks:

- **Filesystem backend** (default) — blobs are written to `BLOSSOM_PATH` using
  an `afero.Fs` (OS filesystem), streamed in 32 KB chunks with a 10-minute
  context timeout.
- **S3 backend** (`STORAGE_BACKEND=s3`) — `s3storage.go` wraps the AWS SDK
  for Go v2 and supports any S3-compatible endpoint (Tigris, AWS S3, MinIO).
  When `S3_PUBLIC_URL` is set, `LoadBlob` returns a redirect URL so a CDN can
  serve the bytes directly; otherwise the blob is streamed through the relay.

`bl.RejectUpload` enforces `MAX_UPLOAD_SIZE_MB` and restricts uploads to
team members. Two extra HTTP endpoints are added for Sakura/client
compatibility:

- `GET /list/<pubkey>` — lists blobs (from S3 or by scanning the blossom
  directory, validating 64-hex filenames and sniffing MIME types).
- `PUT /mirror` — downloads a blob from a remote host, verifies its SHA-256,
  and stores it locally. The remote host must be in `ALLOWED_MIRROR_HOSTS`
  (SSRF protection via `isAllowedMirrorURL`).

### 7. Admin dashboard

`setupDashboardHandlers()` registers an admin UI and API. Endpoints are
mounted under both `/api/dashboard/*` and `/api/admin/*` for compatibility:

| Endpoint                        | Method            | Purpose                                  |
|---------------------------------|-------------------|------------------------------------------|
| `/dashboard`                    | GET               | Serves `public/dashboard.html`.          |
| `/api/{dashboard,admin}/login`  | POST              | Validates the caller's pubkey equals the operator (`_` in `nostr.json`) and sets a `dashboard_session` cookie. |
| `/api/{dashboard,admin}/ui`     | GET               | Serves `templates/dashboard_view.html` (session-gated). |
| `/api/{dashboard,admin}/logout` | *                 | Clears the session cookie.               |
| `/api/{dashboard,admin}/users`  | GET / POST        | List or add team members (local roster only). |
| `/api/{dashboard,admin}/user/`  | PUT / DELETE       | Update or delete a team member.          |
| `/api/{dashboard,admin}/environment` | GET          | Returns non-sensitive env vars (secrets masked). |
| `/api/{dashboard,admin}/convert`| POST              | Converts between npub and hex pubkeys.   |

Session enforcement is handled by `requireAdminSession`, which compares the
`dashboard_session` cookie against `resolveDashboardAdminPubkey()` (in-memory
`_` → local `nostr.json` `_` → `RELAY_PUBKEY` fallback). User mutations are
refused when a remote `NPUB_DOMAIN` is configured, since the local file is not
authoritative in that mode.

### 8. Scheduler

`scheduler.go` implements a lightweight scheduled-post service. Posts are
persisted as JSON in `<DB_PATH>/scheduled_posts.json` by `SchedulerStore`
(mutex-guarded, deep-copied on read to avoid data races).

- `Scheduler.Start()` runs a 1-minute ticker that calls `processPendingPosts()`.
- `ListPending()` atomically transitions due `pending` posts to `processing`
  before handing them to per-post goroutines, preventing duplicate publication.
- `publishPost()` first adds the signed event to the local relay, then
  publishes to each requested external relay. Relay URLs are validated by
  `validateRelayURL()` (scheme + private-network blocking) for SSRF protection.

The HTTP API is mounted at `/api/scheduler/*` and authenticated with NIP-98
(`checkAuth` parses a `Nostr <base64-event>` Authorization header, requires
kind `27235`, and verifies the signature):

| Endpoint                  | Method  | Purpose                                  |
|---------------------------|---------|------------------------------------------|
| `/api/scheduler/schedule` | POST    | Schedule a signed event for future publication. |
| `/api/scheduler/list`     | GET     | List the caller's scheduled posts.       |
| `/api/scheduler/delete`    | DELETE  | Delete one of the caller's scheduled posts. |

### 9. Front page

`frontend.go` defines an in-process HTML template (`frontPageTemplate`)
rendered at `/` for non-WebSocket GET requests. It surfaces relay metadata,
Blossom status, the WebSocket URL, and the team roster source. WebSocket
upgrade requests at `/` are forwarded to the relay so the same port serves
both the UI and the Nostr protocol.

## Configuration

All runtime configuration is loaded from environment variables by
`LoadConfig()` (with `.env` read via `godotenv` but never overriding existing
process env, so Docker/Compose injection wins). The result populates the
`Config` struct in `main.go`.

Key configuration groups:

- **Identity**: `RELAY_NAME`, `RELAY_PUBKEY`, `RELAY_DESCRIPTION`.
- **Team roster**: `TEAM_DOMAIN`, `NPUB_DOMAIN`, `NIP05_PATH`.
- **Storage**: `DB_ENGINE`, `DB_PATH`, `DATABASE_URL` / `POSTGRES_*`.
- **Blossom**: `BLOSSOM_ENABLED`, `BLOSSOM_PATH`, `BLOSSOM_URL`,
  `MAX_UPLOAD_SIZE_MB`, `ALLOWED_MIRROR_HOSTS`.
- **Blob backend**: `STORAGE_BACKEND`, `S3_ENDPOINT`, `S3_BUCKET`, `S3_REGION`,
  `S3_PUBLIC_URL`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`.
- **Access control**: `ALLOWED_KINDS`, `PUBLIC_ALLOWED_KINDS`,
  `TRUSTED_CLIENT_NAME`, `TRUSTED_CLIENT_KINDS`.
- **Rate limiting**: `PUBKEY_RATE_LIMIT`, `IP_RATE_LIMIT`, `CONN_RATE_LIMIT`,
  `QUERY_RATE_LIMIT`.
- **Networking**: `RELAY_PORT`, `WEBSOCKET_URL`.

A complete reference lives in [`ENV_VARIABLES.md`](ENV_VARIABLES.md) and
[`ENV_CONFIG_GUIDE.md`](ENV_CONFIG_GUIDE.md).

## Request lifecycle (event write)

1. Client opens a WebSocket to `RELAY_PORT` and submits a Nostr event.
2. Khatru verifies the event signature and invokes the `RejectEvent` chain.
3. Swarm evaluates, in order: trusted-client exception → delete policy →
   public-kind allowlist → team membership → team-kind allowlist → pubkey rate
   limit → IP rate limit → base64 media rejection.
4. If not rejected, `DBBackend.SaveEvent` (or `ReplaceEvent` for replaceable
   kinds) persists the event.
5. The event is broadcast to subscribers with matching active subscriptions.

## Deployment

Swarm ships as a single binary with no external dependencies beyond the
chosen database (when using Postgres). Deployment options:

- **Bare metal / systemd** — build with `go build -o swarm` and run under
  systemd using an `EnvironmentFile=.env` (see `README.md`).
- **Docker** — `Dockerfile` produces a minimal Alpine image; `docker-compose.yml`
  runs it with the embedded Badger backend.
- **Fly.io** — `fly.toml` (pairs naturally with the Tigris S3 backend).
- **Zeabur** — `zeabur.yaml` and `ZEABUR_DEPLOYMENT.md`.

In all cases a reverse proxy (e.g. nginx, see `setup/nginx-config-update.conf`)
is recommended for TLS termination in front of `RELAY_PORT`.

## Security considerations

- **SSRF protection** on `/mirror` (`isAllowedMirrorURL`) and on scheduler
  relay URLs (`validateRelayURL`): scheme allowlists and private-network
  address blocking.
- **Path traversal** prevention on `/public/*` (`..` rejection).
- **Slow-header / oversized-header** protection via `ReadHeaderTimeout` and
  `MaxHeaderBytes` on the HTTP server.
- **Secret masking** in the dashboard environment endpoint
  (`getEnvironmentVars` masks `password`, `secret`, `key`, `database_url`).
- **Session cookies** are `HttpOnly`, `SameSite=Lax`, and `Secure` when
  `DOCKER_ENV=true`.
- Nostr event signatures are verified by Khatru before storage; the scheduler
  additionally re-verifies signatures on inbound scheduled events.

## Dependencies

The notable external libraries (see `go.mod`):

- [`fiatjaf/khatru`](https://github.com/fiatjaf/khatru) — relay framework and
  Blossom server.
- [`fiatjaf/eventstore`](https://github.com/fiatjaf/eventstore) — Postgres,
  Badger, and LMDB event stores.
- [`nbd-wtf/go-nostr`](https://github.com/nbd-wtf/go-nostr) — Nostr types,
  signing, and relay client (used by the scheduler).
- [`aws/aws-sdk-go-v2`](https://github.com/aws/aws-sdk-go-v2) — S3 backend.
- [`joho/godotenv`](https://github.com/joho/godotenv) — `.env` loading.
- [`spf13/afero`](https://github.com/spf13/afero) — filesystem abstraction for
  Blossom.
