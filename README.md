# Swarm: Nostr Team Relay Software

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

https://github.com/user-attachments/assets/0e2920d1-970f-4d5a-8edd-3211685bf1a8

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
      
      DB_ENGINE="badger" # lmdb, badger, postgres (default: postgres)
      DB_PATH="db/" # only required for badger and lmdb
      
      RELAY_PORT="3334"
      
      POSTGRES_USER=swarm
      POSTGRES_PASSWORD=password
      POSTGRES_DB=relay
      POSTGRES_HOST=localhost
      POSTGRES_PORT=5437
      
      TEAM_DOMAIN="swarm.hivetalk.org" # Domain where the relay / site is served
      NPUB_DOMAIN="hivetalk.org" # Domain that hosts .well-known/nostr.json
      
      BLOSSOM_ENABLED="true"
      BLOSSOM_PATH="blossom/"
      BLOSSOM_URL="http://localhost:3334"
      
      WEBSOCKET_URL="wss://localhost:3334"
      
      # Relay Kind Filtering
      # ALLOWED_KINDS: Restricted to team members only
      # PUBLIC_ALLOWED_KINDS: Any pubkey can post these kinds
      # Leave blank to allow all kinds, or specify comma-separated list of allowed kinds
      # Examples:
      #   ALLOWED_KINDS="" (allow all kinds for team members)
      #   ALLOWED_KINDS="0,1,5,10002,30311" (only allow specific kinds for team members)
      #   PUBLIC_ALLOWED_KINDS="1,6,7" (allow any pubkey to post text notes, reposts, reactions)
      ALLOWED_KINDS=""
      PUBLIC_ALLOWED_KINDS=""
      
      # Trusted client override
      # Events from this client (via ["client","<name>"] tag) are allowed for the
      # configured kinds even if the pubkey is not in nostr.json
      TRUSTED_CLIENT_NAME="The Lookup"
      TRUSTED_CLIENT_KINDS="30017,31990"
      
      # Maximum file upload size in MB (default: 200)
      MAX_UPLOAD_SIZE_MB=200
    ```

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

### Run with Postgres via docker-compose

This repo includes a minimal `docker-compose.yml` for Postgres only. Example flow:

```bash
# 1. Start Postgres (uses .env for credentials/ports)
docker compose up -d postgres

# 2. Build the Swarm image
docker build -t hivetalk/swarm .

# 3. Run Swarm, pointing DB_* env vars at the postgres service
docker run --rm \
  --name swarm-relay \
  -p 3334:3334 \
  --env-file .env \
  --add-host=host.docker.internal:host-gateway \
  hivetalk/swarm
```

You can also create your own `docker-compose` service that uses this image and the same `.env` file.

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
