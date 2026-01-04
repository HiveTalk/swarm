# Environment Variables Configuration Guide

This guide shows the minimum required environment variables for different deployment configurations of the Swarm relay.

## 🎯 Minimum (Essential) - Just to Run

**Only 2 variables required to start the relay:**

```bash
RELAY_PUBKEY="d7416884c7c468ad9e5fd1f2538c7f893318e074998e3538bc827155ef598706"
RELAY_NAME="My Relay"
```

**Default behavior with minimum config:**
- Database: Badger (local file storage)
- Storage: Filesystem (local)
- Blossom: Enabled
- Port: 3334
- Environment: Local development

---

## 🛠️ Optional - Badger + Filesystem

**For local development with Blossom enabled:**

```bash
# Required
RELAY_PUBKEY="d7416884c7c468ad9e5fd1f2538c7f893318e074998e3538bc827155ef598706"
RELAY_NAME="Beeswax"

# Enable Blossom with filesystem storage
BLOSSOM_ENABLED=true
DB_ENGINE=badger
STORAGE_BACKEND=filesystem

# Optional (have good defaults)
DB_PATH=db/
BLOSSOM_PATH=blossom/
BLOSSOM_URL=http://localhost:3777
RELAY_PORT=3777
WEBSOCKET_URL=wss://localhost:3777
```

**Features:**
- ✅ Local Badger database
- ✅ File-based media storage
- ✅ Blossom media server enabled
- ✅ NIP-05 service available
- ✅ Perfect for local testing

---

## 🚀 Ideal - PostgreSQL + S3 Tigris

**For production deployment on Zeabur:**

```bash
# Required
RELAY_PUBKEY="d7416884c7c468ad9e5fd1f2538c7f893318e074998e3538bc827155ef598706"
RELAY_NAME="Beeswax Production"

# Database (PostgreSQL)
DB_ENGINE=postgres
DATABASE_URL=postgres://swarm:password@postgres:5432/relay?sslmode=disable

# Storage (S3/Tigris)
STORAGE_BACKEND=s3
S3_ENDPOINT=https://fly.storage.tigris.dev
S3_BUCKET=swarm-media
S3_REGION=auto
AWS_ACCESS_KEY_ID=tid_xxxxx
AWS_SECRET_ACCESS_KEY=tsec_xxxxx
S3_PUBLIC_URL=https://fly.storage.tigris.dev/swarm-media

# Blossom (enabled for S3)
BLOSSOM_ENABLED=true
BLOSSOM_URL=https://beeswax.hivetalk.org

# Production settings
RELAY_PORT=3334
WEBSOCKET_URL=wss://beeswax.hivetalk.org
DOCKER_ENV=true

# Optional (team features)
NPUB_DOMAIN="hivetalk.org"
TEAM_DOMAIN="beeswax.hivetalk.org"

# Optional (kind filtering)
ALLOWED_KINDS="1,5,30000,30311,30312,30313"
PUBLIC_ALLOWED_KINDS="1,7"
```

**Features:**
- ✅ PostgreSQL database (persistent, scalable)
- ✅ S3/Tigris object storage
- ✅ CDN support for media
- ✅ Production-ready configuration
- ✅ Team domain support
- ✅ Event kind filtering

---

## 📋 Quick Reference Table

| Variable | Minimum | Optional (Badger+FS) | Ideal (PostgreSQL+S3) | Description |
|----------|---------|---------------------|---------------------|-------------|
| `RELAY_PUBKEY` | ✅ Required | ✅ Required | ✅ Required | Relay operator's hex pubkey |
| `RELAY_NAME` | ✅ Required | ✅ Required | ✅ Required | Display name of the relay |
| `DB_ENGINE` | `badger` (default) | `badger` | `postgres` | Database backend |
| `DATABASE_URL` | - | - | ✅ Required | PostgreSQL connection string |
| `STORAGE_BACKEND` | `filesystem` (default) | `filesystem` | `s3` | Media storage backend |
| `S3_*` variables | - | - | ✅ Required | S3/Tigris configuration |
| `BLOSSOM_ENABLED` | `false` (default) | `true` | `true` | Enable Blossom media server |
| `DOCKER_ENV` | `false` (default) | `false` | `true` | Docker environment detection |
| `NPUB_DOMAIN` | - | - | Optional | Domain for NIP-05 verification |
| `TEAM_DOMAIN` | - | - | Optional | Team identification domain |

---

## 🎯 Development vs Production Examples

### Local Development (.env)
```bash
# Ultra-minimal for quick testing
RELAY_PUBKEY="d7416884c7c468ad9e5fd1f2538c7f893318e074998e3538bc827155ef598706"
RELAY_NAME="Dev Relay"

# With Blossom for testing
BLOSSOM_ENABLED=true
DB_ENGINE=badger
```

### Production on Zeabur (Environment Variables)
```bash
# Core required
RELAY_PUBKEY="d7416884c7c468ad9e5fd1f2538c7f893318e074998e3538bc827155ef598706"
RELAY_NAME="Production Relay"

# Database (use Zeabur's PostgreSQL)
DB_ENGINE=postgres
DATABASE_URL=${DATABASE_URL}

# Storage (use Zeabur's S3/Tigris)
STORAGE_BACKEND=s3
S3_ENDPOINT=${S3_ENDPOINT}
S3_BUCKET=${S3_BUCKET}
AWS_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID}
AWS_SECRET_ACCESS_KEY=${AWS_SECRET_ACCESS_KEY}

# Production features
BLOSSOM_ENABLED=true
DOCKER_ENV=true
```

---

## 🔧 Configuration Details

### Database Options

**Badger (Local):**
```bash
DB_ENGINE=badger
DB_PATH=db/
```
- File-based storage
- Good for development
- No external dependencies

**PostgreSQL (Production):**
```bash
DB_ENGINE=postgres
DATABASE_URL=postgres://user:pass@host:5432/db?sslmode=disable
```
- Scalable and persistent
- Better for production
- Requires external database

### Storage Options

**Filesystem (Local):**
```bash
STORAGE_BACKEND=filesystem
BLOSSOM_PATH=blossom/
```
- Local file storage
- Simple setup
- Good for development

**S3/Tigris (Production):**
```bash
STORAGE_BACKEND=s3
S3_ENDPOINT=https://fly.storage.tigris.dev
S3_BUCKET=my-bucket
AWS_ACCESS_KEY_ID=tid_xxxxx
AWS_SECRET_ACCESS_KEY=tsec_xxxxx
```
- Object storage
- CDN support
- Production-ready

### NIP-05 Service

**Basic Setup:**
```bash
NIP05_PATH=public/.well-known/nostr.json
```

**With Custom Domain:**
```bash
NPUB_DOMAIN="mydomain.com"
```

---

## 🚀 Getting Started

1. **Copy the appropriate configuration** above to your `.env` file
2. **Set your RELAY_PUBKEY** to your actual Nostr pubkey
3. **Choose your database and storage** backend
4. **Run the relay**: `./swarm` or `docker-compose up`

That's it! The relay will start with your chosen configuration. 🎉

---

## 📚 Additional Resources

- [ZEABUR_DEPLOYMENT.md](ZEABUR_DEPLOYMENT.md) - Zeabur-specific deployment guide
- [ENV_VARIABLES.md](ENV_VARIABLES.md) - Complete environment variable reference
- [ENV_TEMPLATE.txt](ENV_TEMPLATE.txt) - Template for Zeabur environment variables
