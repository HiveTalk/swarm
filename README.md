# Swarm: Nostr Team Relay Software

This relay software provides a Nostr relay to a team.  This is a fork of the bitvora [team-relay](https://github.com/bitvora/team-relay) with  modifications for Swarm.hivetalk.org 

In the .env file, the team domain is used to reject non team members, only members in nostr.json are allowed for the specified team domain.

Additional features we added for production use:
- Blossom
   - added read and write timeouts
   - prevent slow header attacks, max header size
   - max size upload
   - added /mirror endpoint to allow for syncing content with other relays
   - added /list endpoint to allow for listing content for a specific user
- Relay Kinds - add support to limit kinds allowed, kinds specified in .env file
- Frontend
   - added front page with relay and blossom information
   - added Bouquet integration, to enable media upload and syncing with other relays.

<img width="1075" height="682" alt="Screenshot 2025-08-16 at 6 32 59 PM" src="https://github.com/user-attachments/assets/30ac25d6-658e-411d-a656-317e51053d0e" />

https://github.com/user-attachments/assets/0e2920d1-970f-4d5a-8edd-3211685bf1a8

## Table of Contents

- [Prerequisites](#prerequisites)
- [Setting Environment Variables](#setting-environment-variables)
- [Compiling the Application](#compiling-the-application)
- [Running the Application as a Service](#running-the-application-as-a-service)
- [Running Docker](#running-docker)


## Prerequisites

- A Linux-based operating system
- Go installed on your system
- A Webserver (like nginx) if blossom is enabled

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
      # Leave blank to allow all kinds, or specify comma-separated list of allowed kinds
      # Examples:
      #   ALLOWED_KINDS="" (allow all kinds)
      #   ALLOWED_KINDS="0,1,5,10002,30311" (only allow specific kinds)
      ALLOWED_KINDS=""
      
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
