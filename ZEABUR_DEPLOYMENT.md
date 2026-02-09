# Zeabur Deployment Guide

## Deployment Modes

Swarm supports two deployment modes controlled by the `BUILD_CMS` build arg:

### CMS + Relay (default)

Deploys the full stack: Nostr relay + nostr-cms content manager at `/`.

The CMS **auto-detects** the relay WebSocket URL from the current domain at runtime,
so `VITE_DEFAULT_RELAY` is no longer required. The only optional build arg is:

| Build Arg | Purpose | Example |
|-----------|---------|--------|
| `BUILD_CMS` | Enable CMS build | `true` (default) |
| `VITE_MASTER_PUBKEY` | Site owner hex pubkey (optional) | `8ad8f1f...` |

**Zeabur:** Just deploy the `cms-integration` branch — no environment variables needed for the CMS.
Optionally set `VITE_MASTER_PUBKEY` if you want the CMS to sync config from a master pubkey.

**Docker Compose:**
```bash
# Clone with submodules
git clone --recurse-submodules https://github.com/HiveTalk/swarm.git
cd swarm

# Deploy CMS + Relay (zero config — relay URL auto-detected)
docker compose up -d
```

### Relay Only

Deploys just the Nostr relay, Blossom, scheduler, and dashboard — no CMS frontend.
Root `/` redirects to the relay info page at `/relay-info`.

| Build Arg | Purpose | Value |
|-----------|---------|-------|
| `BUILD_CMS` | Disable CMS build | `false` |

**Zeabur:** Set `BUILD_CMS=false` in your environment variables. No submodule initialization needed.

**Docker Compose:**
```bash
# Submodules not required for relay-only
git clone https://github.com/HiveTalk/swarm.git
cd swarm

# Deploy relay only
BUILD_CMS=false docker compose up -d
```

## Persistent Data Strategy

### Problem
Docker containers are ephemeral - when you deploy a new version, the old container is destroyed and replaced, wiping out any data stored in the container's filesystem.

### Solutions

#### Option 1: Automatic Volume Creation (Recommended)

**Method A: Using `zeabur.yaml` (Best)**
Create `zeabur.yaml` in your repository root:

```yaml
volumes:
  - name: swarm-db
    mountPath: /app/db
    size: 1Gi
    type: persistent
  - name: swarm-blossom
    mountPath: /app/blossom
    size: 10Gi
    type: persistent
  - name: swarm-backups
    mountPath: /app/backups
    size: 5Gi
    type: persistent
```

**Method B: Using Docker Labels**
The Dockerfile includes Zeabur-specific labels that automatically create volumes:
- `zeabur.volume.db.size="1Gi"` → `/app/db`
- `zeabur.volume.blossom.size="10Gi"` → `/app/blossom`
- `zeabur.volume.backups.size="5Gi"` → `/app/backups`

**Method C: Using Docker Compose Labels**
Updated `docker-compose.yml` with Zeabur labels for automatic volume creation.

#### Option 2: Manual Volume Configuration

1. **Configure Zeabur Volumes** in dashboard:
   - Add persistent volumes for:
     - `/app/db` (for Badger database)
     - `/app/blossom` (for Blossom file storage)

2. **Set Environment Variables**:
   ```bash
   DB_ENGINE=badger
   DB_PATH=/app/db/
   BLOSSOM_ENABLED=true
   BLOSSOM_PATH=/app/blossom/
   STORAGE_BACKEND=filesystem
   ```

#### Option 3: Use External Services (Production Recommended)

1. **PostgreSQL Database**:
   ```bash
   DB_ENGINE=postgres
   DATABASE_URL=postgresql://user:pass@host:port/dbname
   ```

2. **S3 Storage for Blossom**:
   ```bash
   STORAGE_BACKEND=s3
   S3_ENDPOINT=https://s3.amazonaws.com
   S3_BUCKET=your-bucket-name
   AWS_ACCESS_KEY_ID=your-access-key
   AWS_SECRET_ACCESS_KEY=your-secret-key
   ```

#### Option 4: Backup/Restore Strategy (Built-in)

The Dockerfile includes automatic backup/restore scripts:

- **Backup**: `/app/backup.sh` - Creates compressed backups
- **Restore**: `/app/restore.sh` - Restores from latest backup
- **Auto-restore**: Runs on startup if data directories are empty

### Automatic Volume Creation Details

**How it works:**
1. Zeabur reads labels from Docker image during deployment
2. Automatically creates persistent volumes with specified sizes
3. Mounts volumes to specified paths in container
4. Volumes persist across container redeployments

**Volume Sizes:**
- **Database (`/app/db`)**: 1Gi (sufficient for Badger)
- **Blossom (`/app/blossom`)**: 10Gi (for file uploads)
- **Backups (`/app/backups`)**: 5Gi (for backup rotation)

**Labels Used:**
```dockerfile
LABEL zeabur.volume.db.size="1Gi"
LABEL zeabur.volume.db.type="persistent"
LABEL zeabur.volume.db.mount-path="/app/db"
```

### Manual Backup Commands

```bash
# Create backup
docker exec <container_name> /app/backup.sh

# Restore from backup  
docker exec <container_name> /app/restore.sh

# View backups
docker exec <container_name> ls -la /app/backups/
```

### Zeabur-Specific Configuration

1. **Environment Variables** in Zeabur:
   - Set all required variables from `.env.example`
   - Include GitHub token for NIP-05 service:
     ```
     GITHUB_TOKEN=your_github_token
     GITHUB_OWNER=your_github_username
     GITHUB_REPO=your_repo_name
     ```

2. **Automatic Volumes** (if using labels/zeabur.yaml):
   - Volumes created automatically on first deployment
   - No manual configuration needed
   - Verify in Zeabur dashboard under "Storage"

3. **Port Configuration**:
   - External port: 3334
   - Internal port: 3334

### Deployment Steps

1. **Push code** to GitHub
2. **Connect repository** to Zeabur
3. **Configure environment variables** (if not in zeabur.yaml)
4. **Deploy** - volumes will be created automatically
5. **Verify** volumes in Zeabur dashboard

### Monitoring

- Health check endpoint: `http://your-domain:3334/api/health`
- CMS home page: `http://your-domain:3334/`
- Relay info page: `http://your-domain:3334/relay-info`
- Logs available in Zeabur dashboard
- Backup files stored in `/app/backups/` inside container
- Volume usage visible in Zeabur storage section

### Troubleshooting

**Volumes not created automatically?**
- Check Zeabur supports label-based volume creation
- Verify labels are present in built image: `docker image inspect`
- Use manual volume configuration as fallback

**Data lost after deployment?**
- Check volume mount paths match container paths
- Verify volume type is "persistent"
- Check container logs for backup/restore messages
- Ensure volumes are not being recreated on each deploy

**Database errors?**
- Ensure `/app/db` volume is mounted and persistent
- Check permissions on database directory
- Consider switching to PostgreSQL for production

**Blossom uploads lost?**
- Ensure `/app/blossom` volume is mounted and persistent
- Check `BLOSSOM_PATH` environment variable
- Consider S3 storage for production

**Volume size issues?**
- Monitor usage in Zeabur dashboard
- Increase size in labels/zeabur.yaml
- Clean up old backups automatically
