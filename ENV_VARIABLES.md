# Environment Variables Reference

This document lists all environment variables that can be configured for the Swarm relay.

## Development Environment

| Variable | Description | Default |
|----------|-------------|---------|
| `DOCKER_ENV` | Set to `true` when running in Docker, `false` for local development | `false` |

## Required Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `RELAY_NAME` | Display name of the relay | `"Swarm Relay"` |
| `RELAY_PUBKEY` | Hex pubkey of the relay operator | `"8ad8f1f78c8e11966242e28a7ca15c936b23a999d5fb91bfe4e4472e2d6eaf55"` |
| `RELAY_DESCRIPTION` | Description shown in relay info | `"Team Nostr relay"` |

## Server Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `RELAY_PORT` | Port the relay listens on | `3334` |
| `WEBSOCKET_URL` | WebSocket URL for frontend clients | `wss://localhost:3334` |

## Database Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `DB_ENGINE` | Database engine: `badger`, `lmdb`, or `postgresql` | `postgres` |
| `DB_PATH` | Path for local database storage (badger/lmdb only) | `db/` |

### PostgreSQL (when `DB_ENGINE=postgresql`)

| Variable | Description |
|----------|-------------|
| `DATABASE_URL` | Full PostgreSQL connection URL (takes precedence over individual vars) |
| `POSTGRES_USER` | PostgreSQL username |
| `POSTGRES_PASSWORD` | PostgreSQL password |
| `POSTGRES_DB` | PostgreSQL database name |
| `POSTGRES_HOST` | PostgreSQL host |
| `POSTGRES_PORT` | PostgreSQL port |

## Team & Domain Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `TEAM_DOMAIN` | Domain for team identification | `""` (empty) |
| `NPUB_DOMAIN` | Domain to fetch `.well-known/nostr.json` for team members | `""` (empty) |

## Blossom Media Storage

| Variable | Description | Default |
|----------|-------------|---------|
| `BLOSSOM_ENABLED` | Enable Blossom media storage | `false` |
| `BLOSSOM_PATH` | Local filesystem path for media (when using filesystem backend) | `blossom/` |
| `BLOSSOM_URL` | Public URL for Blossom service | `http://localhost:3334` |
| `MAX_UPLOAD_SIZE_MB` | Maximum file upload size in MB | `200` |
| `ALLOWED_MIRROR_HOSTS` | Comma-separated list of allowed hosts for `/mirror` endpoint | `""` (disabled) |

## S3/Tigris Storage (Optional)

Use these when you want to store media in S3-compatible storage instead of the local filesystem.

| Variable | Description | Default |
|----------|-------------|---------|
| `STORAGE_BACKEND` | Storage backend: `filesystem` or `s3` | `filesystem` |
| `S3_ENDPOINT` | S3 endpoint URL | `""` |
| `S3_BUCKET` | S3 bucket name | `""` |
| `S3_REGION` | S3 region | `auto` |
| `S3_PUBLIC_URL` | Public URL for CDN redirect (optional) | `""` |
| `AWS_ACCESS_KEY_ID` | AWS/S3 access key ID | (required for S3) |
| `AWS_SECRET_ACCESS_KEY` | AWS/S3 secret access key | (required for S3) |

### Tigris S3 Configuration

**API Endpoints (`S3_ENDPOINT`):**
- **On Fly.io:** `https://fly.storage.tigris.dev`
- **Outside Fly.io:** `https://t3.storage.dev`

**Public URL (`S3_PUBLIC_URL`):**
- `https://fly.storage.tigris.dev/your-bucket`

## NIP-05 Service Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `NIP05_PATH` | Path to nostr.json file for NIP-05 registrations | `public/.well-known/nostr.json` |

**Path Examples:**
- **Local Development:** `public/.well-known/nostr.json`
- **Docker:** `/app/public/.well-known/nostr.json`

## Event Kind Filtering

| Variable | Description | Default |
|----------|-------------|---------|
| `ALLOWED_KINDS` | Comma-separated list of event kinds allowed for team members | `""` (all kinds) |
| `PUBLIC_ALLOWED_KINDS` | Comma-separated list of event kinds allowed for public users | `""` (none) |

## Spam Rate Limiting (Optional)

If a rate-limit variable is unset, empty, invalid, or non-positive, that specific limiter is disabled.

| Variable | Description | Default |
|----------|-------------|---------|
| `PUBKEY_RATE_LIMIT` | Max events per minute per pubkey (non-team users) | disabled |
| `IP_RATE_LIMIT` | Max events per minute per IP | disabled |
| `CONN_RATE_LIMIT` | Max websocket connections per 2 minutes per IP | disabled |
| `QUERY_RATE_LIMIT` | Max queries per minute per IP | disabled |

## Trusted Client Override

| Variable | Description | Default |
|----------|-------------|---------|
| `TRUSTED_CLIENT_NAME` | Client tag name to trust | `""` |
| `TRUSTED_CLIENT_KINDS` | Comma-separated kinds allowed for trusted client, or `"*"` for all | `""` |

## X/Twitter Crossposting (Optional)

When configured, team members can link their X account from the CMS admin
panel (Crossposting tab) and have their kind-1 notes automatically posted to
X — both notes published live and notes published by the scheduler. Replies
(any `e` tag) and non-kind-1 events are never crossposted.

Two modes, pick one:

| Variable | Description | Default |
|----------|-------------|---------|
| `XPOST_SERVICE_URL` | Base URL of a shared `xpost` service (delegate mode — recommended) | `""` |
| `TWITTER_KEY` | X app OAuth 1.0a consumer key (local mode) | `""` (feature dormant) |
| `TWITTER_KEY_SECRET` | X app OAuth 1.0a consumer secret (local mode) | `""` |

If `XPOST_SERVICE_URL` is set it takes precedence; `TWITTER_KEY*` are then
only used as a fallback for accounts already linked in local mode.

### Delegate mode (recommended): the shared `xpost` service

One `xpost` deployment holds a single X developer app and serves any number of
relays — relay operators do **not** need their own X credentials. See
`docs/XPOST.md` for the full setup walkthrough.

Run it (`go build ./cmd/xpost`) with:

| Variable | Description | Default |
|----------|-------------|---------|
| `TWITTER_KEY` | X app OAuth 1.0a consumer key | required |
| `TWITTER_KEY_SECRET` | X app OAuth 1.0a consumer secret | required |
| `XPOST_PUBLIC_URL` | Externally reachable base URL, e.g. `https://xpost.example.com` | required |
| `PORT` | Listen port | `8090` |
| `XPOST_DB_PATH` | Directory for `xpost_accounts.json` | `./xpost-data` |

Register `https://<xpost-host>/v1/link/callback` as the X app's Callback URI,
then set `XPOST_SERVICE_URL=https://<xpost-host>` on each relay.

### Local mode

**X developer app setup:**
1. Create an app in the [X developer portal](https://developer.x.com/) — it must
   be part of a project, not standalone.
2. App permissions: **Read and write**.
3. App type: **Web App, Automated App or Bot**.
4. Callback URI: `https://<your-relay-host>/api/x/auth/callback` (must match
   the host the CMS calls the API on — usually your site domain).
5. Copy the app's **API Key and Secret** into `TWITTER_KEY` /
   `TWITTER_KEY_SECRET`.

The callback host is derived per-request (with `X-Forwarded-*` support), so no
extra config is needed for unified deployments; split setups just register the
API host's callback URL. Linked account tokens are stored in
`<DB_PATH>/x_accounts.json`. X's free API tier is limited (~500 posts/month) —
check current limits for your tier.

---

## Example Configurations

### Minimal Local Setup
```env
DOCKER_ENV=false
RELAY_NAME="My Relay"
RELAY_PUBKEY="your-hex-pubkey"
RELAY_DESCRIPTION="My personal relay"
DB_ENGINE=badger
DB_PATH=db/
NIP05_PATH=public/.well-known/nostr.json
```

### Production PostgreSQL Setup
```env
DOCKER_ENV=true
DB_ENGINE=postgres
DATABASE_URL=postgres://swarm:password@postgres:5432/relay?sslmode=disable
NIP05_PATH=/app/public/.well-known/nostr.json
```

### With Blossom (Filesystem)
```env
BLOSSOM_ENABLED=true
BLOSSOM_PATH=blossom/
BLOSSOM_URL=https://myrelay.com
STORAGE_BACKEND=filesystem
```

### With Blossom (Tigris S3)
```env
BLOSSOM_ENABLED=true
BLOSSOM_URL=https://myrelay.com
STORAGE_BACKEND=s3
S3_ENDPOINT=https://fly.storage.tigris.dev
S3_BUCKET=my-bucket
S3_REGION=auto
AWS_ACCESS_KEY_ID=tid_xxxxx
AWS_SECRET_ACCESS_KEY=tsec_xxxxx
S3_PUBLIC_URL=https://fly.storage.tigris.dev/my-bucket
```

### Team Relay with Kind Filtering
```env
NPUB_DOMAIN=myteam.com
ALLOWED_KINDS=1,5,30000,30311,30312,30313
PUBLIC_ALLOWED_KINDS=1,7
```
