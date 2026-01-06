# Swarm: Nostr Team Relay Software

[![Deployed on Zeabur](https://zeabur.com/deployed-on-zeabur-dark.svg)](https://zeabur.com/referral?referralCode=bitkarrot&utm_source=bitkarrot&utm_campaign=oss)

This relay software provides a Nostr relay to a team.  This is a fork of the bitvora [team-relay](https://github.com/bitvora/team-relay) with  modifications for Swarm.hivetalk.org 

In the .env file, the team domain is used to reject non team members, only members in nostr.json are allowed for the specified team domain.

Additional features we added for production use:
- **Enhanced Access Control System**
   - **Public Posting**: Configure `PUBLIC_ALLOWED_KINDS` to allow any pubkey to post specific event kinds (e.g., text notes, reactions)
   - **Team Member Privileges**: `ALLOWED_KINDS` remains restricted to team members only
   - **Hierarchical Access**: Trusted clients → Public users → Team members with escalating permissions
   - **Delete Capabilities**: Public users can delete their own posts, team members can delete any events
- **Rate Limiting & Spam Protection**
   - **Pubkey Rate Limiting**: 5 events/minute for non-team members
   - **IP Rate Limiting**: 10 events/minute per IP address
   - **Connection Rate Limiting**: 2 connections per 2 minutes per IP
   - **Team Member Exemption**: Team members bypass pubkey rate limits
- **Trusted Client Support**
   - Configure `TRUSTED_CLIENT_NAME` and `TRUSTED_CLIENT_KINDS` for special client access
   - Events from trusted clients bypass normal restrictions for specified kinds
- **Blossom Media Server**
   - Added read and write timeouts
   - Prevent slow header attacks, max header size
   - Max size upload configuration
   - Added `/mirror` endpoint to allow for syncing content with other relays
   - Added `/list` endpoint to allow for listing content for a specific user
   - **S3/Tigris Storage Backend**: Optional S3-compatible storage for media files (Tigris, AWS S3, MinIO, etc.)
- **Relay Kind Filtering**
   - Support to limit kinds allowed, kinds specified in .env file
   - Separate configuration for public vs team member allowed kinds
- **Frontend Enhancements**
   - Added front page with relay and blossom information
   - Added Bouquet integration, to enable media upload and syncing with other relays
   - Curator client integration for enhanced content management
- **Docker Support**
   - Full containerization support with Dockerfile
   - Docker Compose integration for easy deployment
   - Multi-architecture build support

<img width="1075" height="682" alt="Screenshot 2025-08-16 at 6 32 59 PM" src="https://github.com/user-attachments/assets/30ac25d6-658e-411d-a656-317e51053d0e" />

## Table of Contents

- [Prerequisites](#prerequisites)
- [Access Control System](#access-control-system)
- [Rate Limiting & Security](#rate-limiting--security)
- [Setting Environment Variables](#setting-environment-variables)
- [Compiling the Application](#compiling-the-application)
- [Running the Application as a Service](#running-the-application-as-a-service)
- [Running Docker](#running-docker)


## Prerequisites

- A Linux-based operating system
- Go installed on your system
- A Webserver (like nginx) if blossom is enabled

## Access Control System

Swarm implements a hierarchical access control system with three levels of access:

### 1. **Trusted Clients** (Highest Priority)
- Configure via `TRUSTED_CLIENT_NAME` and `TRUSTED_CLIENT_KINDS`
- Events from trusted clients (identified by `["client","<name>"]` tag) bypass normal restrictions
- Useful for allowing specific applications to post certain event kinds regardless of pubkey

### 2. **Public Users** (Medium Priority)
- Configure via `PUBLIC_ALLOWED_KINDS` to specify which event kinds any pubkey can post
- Can delete their own posts (kind 5 events)
- Example: `PUBLIC_ALLOWED_KINDS="1,6,7"` allows any user to post text notes, reposts, and reactions

### 3. **Team Members** (Full Access)
- Configure via `ALLOWED_KINDS` for team-member-only event kinds
- Team members (listed in nostr.json) have access to both `ALLOWED_KINDS` and `PUBLIC_ALLOWED_KINDS`
- Can delete any events on the relay
- Bypass all rate limiting restrictions

## Rate Limiting & Security

Swarm includes comprehensive rate limiting and spam protection:

### **Pubkey Rate Limiting**
- **5 events/minute** for non-team members
- Team members are exempt from pubkey rate limits
- Prevents spam from individual accounts

### **IP Rate Limiting**
- **10 events/minute** per IP address
- **2 connections per 2 minutes** per IP
- Prevents abuse from single IP addresses

### **Team Member Exemptions**
- Team members bypass pubkey rate limits
- Ensures team operations are never throttled
- Maintains relay performance for authorized users

## Setting Environment Variables

1.  Create a `.env` file in the root directory of your project.

2.  Add your environment variables to the `.env` file. For example:

    ```env
    RELAY_NAME="Swarm"
    RELAY_PUBKEY="8ad8f1f78c8e11966242e28a7ca15c936b23a999d5fb91bfe4e4472e2d6eaf55"
    RELAY_DESCRIPTION="Swarm Hivetalk Team Relay"
    
    TEAM_DOMAIN="swarm.hivetalk.org" # Optional: Domain where the relay / site is served
    NPUB_DOMAIN="hivetalk.org" # Optional: Domain that hosts .well-known/nostr.json (falls back to public/.well-known/nostr.json if not set)
    
    DB_ENGINE="postgres"
    # DB_ENGINE="badger" # lmdb, badger, postgres (default: postgres)
    # DB_PATH="db/" # only required for badger and lmdb
    
    # Option 1: FOR POSTGRES ONLY: Use DATABASE_URL (takes precedence if set)
    DATABASE_URL=postgres://swarm:password@localhost:5437/relay?sslmode=disable
    
    # Option 2: Use individual postgres variables (used if DATABASE_URL is not set)
    # POSTGRES_USER=swarm
    # POSTGRES_PASSWORD=password
    # POSTGRES_DB=relay
    # POSTGRES_HOST=localhost
    # POSTGRES_PORT=5437
    
    RELAY_PORT="3334"
    
    BLOSSOM_ENABLED="false"
    BLOSSOM_PATH="blossom/"
    BLOSSOM_URL="http://localhost:3334"
    
    WEBSOCKET_URL="wss://localhost:3334"
    
    # Relay Kind Filtering
    # ALLOWED_KINDS: Restricted to team members only (blank = allow all kinds for team members)
    # PUBLIC_ALLOWED_KINDS: Any pubkey can post these kinds (blank = public cannot post, only team members)
    # Specify comma-separated list of allowed kinds
    # Examples:
    #   ALLOWED_KINDS="" (allow all kinds for team members)
    #   ALLOWED_KINDS="0,1,5,10002,30311" (only allow specific kinds for team members)
    #   PUBLIC_ALLOWED_KINDS="1,6,7" (allow any pubkey to post text notes, reposts, reactions)
    ALLOWED_KINDS=""
    PUBLIC_ALLOWED_KINDS= # (blank = public cannot post, only team members)
    
    # Trusted client override
    # Events from this client (via ["client","<name>"] tag) are allowed for the
    # configured kinds even if the pubkey is not in nostr.json
    # Set TRUSTED_CLIENT_KINDS="all" to allow any kind from the trusted client or as comma separated list of kinds
    TRUSTED_CLIENT_NAME=""
    TRUSTED_CLIENT_KINDS="all"
    
    # Maximum file upload size in MB (default: 200)
    MAX_UPLOAD_SIZE_MB=200
    
    # Allowed hosts for the /mirror endpoint (SSRF protection)
    # Leave blank to disable the mirror endpoint entirely
    # Specify comma-separated list of allowed hosts (with port if non-standard)
    # Examples:
    #   ALLOWED_MIRROR_HOSTS="blossom.example.com,cdn.example.org"
    #   ALLOWED_MIRROR_HOSTS="blossom.primal.net,cdn.satellite.earth"
    ALLOWED_MIRROR_HOSTS=
    
    # S3/Tigris Storage Backend (optional - defaults to filesystem)
    # Use "s3" to store media in S3-compatible storage instead of local filesystem
    STORAGE_BACKEND="filesystem"  # or "s3"
    
    # S3 Configuration (only required when STORAGE_BACKEND="s3")
    # For Tigris on Fly.io: https://fly.storage.tigris.dev
    # For Tigris outside Fly.io: https://t3.storage.dev
    S3_ENDPOINT=""
    S3_BUCKET=""
    S3_REGION="auto"
    AWS_ACCESS_KEY_ID=""
    AWS_SECRET_ACCESS_KEY=""
    S3_PUBLIC_URL=""  # Optional: https://fly.storage.tigris.dev/your-bucket
    ```

For a complete list of all environment variables, see [ENV_VARIABLES.md](ENV_VARIABLES.md).

## Compiling the Application

1. Clone the repository:

   ```bash
   git clone https://github.com/hivetalk/swarm.git
   cd swarm
   ```

2. Build the application:


## 🚀 Quick Setup

### 1. Build the Bouquet Client

```bash
# Option A: Use the build script (recommended)
./build-bouquet.sh

# Option B: Manual build
cd clients/bouquet
pnpm install
pnpm run build:integration
cd ../..
```

### 2. Start the Go Server

```bash
# Build and run the Go server
go build -o swarm
./swarm
```

If any issues with building for lmdb on ubuntu:

```bash
sudo apt-get update
sudo apt-get install -y liblmdb-dev build-essential
```

More details about Bouquet integration can be found in the [BOUQUET_INTEGRATION.md](BOUQUET_INTEGRATION.md) file.

## Running Docker

### Build the image

From the repo root:

```bash
docker build -t hivetalk/swarm .
```

### Run with local .env

Make sure you have a `.env` file in the project root (see [Setting Environment Variables](#setting-environment-variables)), then run:

```bash
docker run --rm \
  --name swarm-relay \
  -p 3334:3334 \
  --env-file .env \
  hivetalk/swarm
```

If you change `RELAY_PORT` in `.env`, update the `-p` mapping accordingly (e.g. `-p 7447:7447`).

### Run with docker-compose (recommended)

The `docker-compose.yml` includes both Postgres and the Swarm relay. Environment variables are read from a `.env` file with sensible defaults.

```bash
# 1. Copy and customize environment variables
cp .env.example .env
# Edit .env with your values

# 2. Start everything
docker compose up -d

# Or start with build
docker compose up -d --build
```

To override specific variables without editing `.env`:
```bash
RELAY_NAME="My Relay" docker compose up -d
```

To use a different env file:
```bash
docker compose --env-file .env.production up -d
```

## Running the Application as a Service

1. Create a systemd service file:

   ```bash
   sudo nano /etc/systemd/system/team-relay.service
   ```

2. Add the following content to the service file: (update paths and usernames as needed)

   ```ini
   [Unit]
   Description=Team Relay
   After=network.target

   [Service]
   ExecStart=/path/to/yourappname
   WorkingDirectory=/path/to/team-relay
   EnvironmentFile=/path/to/team-relay/.env
   Restart=always
   User=ubuntu

   [Install]
   WantedBy=multi-user.target
   ```

3. Reload the systemd daemon:

   ```bash
   sudo systemctl daemon-reload
   ```

4. Enable and start the service:

   ```bash
   sudo systemctl enable team-relay
   sudo systemctl start team-relay
   ```

5. Check the status of the service:

   ```bash
   sudo systemctl status team-relay
   ```

## Conclusion

Your team relay will be running at localhost:3334. Feel free to serve it with nginx or any other reverse proxy.
