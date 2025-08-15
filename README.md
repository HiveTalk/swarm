# Swarm: Nostr Team Relay Software

This is a fork of the bitvora team-relay with modifications. 
This relay software specializes in providing a Nostr relay to a team. 
This guide will help you set up and run the software on a Linux machine.

Also forked a copy of Front ends for testing use with the swarm blossom relay,
bouquet and sakura. They are early prototypes so beware when syncing, there
might be issues. Bouquet is good for bulk syncing, Sakura is good for individual file mirroring. 

Buggy Notes:
- Sakura syncs images well but videos might sync to type application/octet-stream and then they are binaries, will need to fix this on tha sakura
- Sakura takes a really long time to login be patient
- Fixed some syncing issue bugs with Sakura, as well as refresh page will not log user out anymore
- Bouquet syncing is inconsistent, unclear why
- Bouquet - there is a cors error on the bouquet on vercel but not on original bouquet

WARNING: Swarm server is small, so we might run out of space, also max size per upload is 100MB

Please do test it out as proof of concept


## Table of Contents

- [Prerequisites](#prerequisites)
- [Setting Environment Variables](#setting-environment-variables)
- [Running Docker](#running-docker)
- [Installing Go](#installing-go)
- [Compiling the Application](#compiling-the-application)
- [Running the Application as a Service](#running-the-application-as-a-service)

## Prerequisites

- A Linux-based operating system
- Go installed on your system
- A Webserver (like nginx) if blossom is enabled

## Setting Environment Variables

1.  Create a `.env` file in the root directory of your project.

2.  Add your environment variables to the `.env` file. For example:

    ```env

    RELAY_NAME="Bitvora"
    RELAY_PUBKEY="8ad8f1f78c8e11966242e28a7ca15c936b23a999d5fb91bfe4e4472e2d6eaf55"
    RELAY_DESCRIPTION="Bitvora Team Relay"

    DB_ENGINE="lmdb" # lmdb, badger, postgres
    DB_PATH="db/" # only needed for lmdb, badger

   # only needed for postgres
    POSTGRES_USER=bitvora
    POSTGRES_PASSWORD=password
    POSTGRES_DB=relay
    POSTGRES_HOST=localhost
    POSTGRES_PORT=5437

    TEAM_DOMAIN="bitvora.com"
    BLOSSOM_ENABLED="true"
    BLOSSOM_PATH="blossom/"
    BLOSSOM_URL="http://localhost:3334"

    ```

## Compiling the Application

1. Clone the repository:

   ```bash
   git clone https://github.com/bitvora/team-relay.git
   cd team-relay
   ```

2. Build the application:

   ```bash
   go build -o team-relay
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
