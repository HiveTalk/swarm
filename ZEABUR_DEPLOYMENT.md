# Zeabur Deployment Guide

## Persistent Data Strategy

### Problem
Docker containers are ephemeral - when you deploy a new version, the old container is destroyed and replaced, wiping out any data stored in the container's filesystem.

### Solutions

#### Option 1: Use Zeabur's Persistent Storage (Recommended)

1. **Configure Zeabur Volumes**:
   - In Zeabur dashboard, add persistent volumes for:
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

#### Option 2: Use External Services (Production Recommended)

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

#### Option 3: Backup/Restore Strategy (Built-in)

The Dockerfile includes automatic backup/restore scripts:

- **Backup**: `/app/backup.sh` - Creates compressed backups
- **Restore**: `/app/restore.sh` - Restores from latest backup
- **Auto-restore**: Runs on startup if data directories are empty

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

2. **Persistent Volumes**:
   - Mount Zeabur persistent storage to:
     - `/app/db`
     - `/app/blossom`

3. **Port Configuration**:
   - External port: 3334
   - Internal port: 3334

### Deployment Steps

1. Push code to GitHub
2. Connect repository to Zeabur
3. Configure environment variables
4. Set up persistent volumes
5. Deploy

### Monitoring

- Health check endpoint: `http://your-domain:3334/`
- Logs available in Zeabur dashboard
- Backup files stored in `/app/backups/` inside container

### Troubleshooting

**Data lost after deployment?**
- Check Zeabur volume configuration
- Verify volume mount paths
- Check container logs for backup/restore messages

**Database errors?**
- Ensure `/app/db` volume is mounted
- Check permissions on database directory
- Consider switching to PostgreSQL for production

**Blossom uploads lost?**
- Ensure `/app/blossom` volume is mounted  
- Check `BLOSSOM_PATH` environment variable
- Consider S3 storage for production
