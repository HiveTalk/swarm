# Swarm: Nostr Team Relay Software

This is a fork of the [bitvora](https://github.com/bitvora/team-relay) team-relay with modifications for Swarm.hivetalk.org This relay software specializes in providing a Nostr relay to a team.  This guide will help you set up and run the software on a Linux machine.

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

TODO: 
- Turn the front end of the relay into an upload and sync client similar to bouquet w/nostr login
- Add an admin panel that allows administration of users and policies.

<img width="1075" height="682" alt="Screenshot 2025-08-16 at 6 32 59 PM" src="https://github.com/user-attachments/assets/30ac25d6-658e-411d-a656-317e51053d0e" />

<img width="1085" height="681" alt="Screenshot 2025-08-16 at 6 33 07 PM" src="https://github.com/user-attachments/assets/25e6be60-a0a5-4091-a966-0312f7d6d280" />

## For Testing
For convenience, this repository contains a copy of frontends for testing with the swarm blossom relay, these two are called bouquet and sakura and a copy reside in the clients directory. Both have been modified to fix issues as of 15 AUG 2025. They are early prototypes and not actively maintained upstream, so beware when syncing, there might be issues. Bouquet is good for bulk syncing, Sakura is good for individual file mirroring.

Buggy Client Notes:
- Sakura syncs images well but videos might sync to type application/octet-stream and then they are binaries, will need to fix this.
- Sakura takes a really long time to login, be patient
- Fixed some syncing issue bugs with Sakura, as well as refresh page, so it will not logout user.
- Bouquet syncing can be inconsistent, needs investigation
- Bouquet - there is a cors error on the bouquet deploy on vercel
- works with alby and nos2x, may have issues with nos2x-fox

Live instances at:
- https://sakura-beta-ochre.vercel.app/
- https://bouquet.slidestr.net/

  
Mirror files
https://github.com/user-attachments/assets/5a0847d3-49f1-406a-bea2-9487c5b37318

Upload
https://github.com/user-attachments/assets/89e400e8-7816-4739-9690-ddc28135b5a6



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

    RELAY_NAME="Swarm"
    RELAY_PUBKEY="8ad8f1f78c8e11966242e28a7ca15c936b23a999d5fb91bfe4e4472e2d6eaf55"
    RELAY_DESCRIPTION="Swarm Team Relay"

    DB_ENGINE="lmdb" # lmdb, badger, postgres
    DB_PATH="db/" # only needed for lmdb, badger

   # only needed for postgres
    POSTGRES_USER=swarm
    POSTGRES_PASSWORD=password
    POSTGRES_DB=relay
    POSTGRES_HOST=localhost
    POSTGRES_PORT=5437

    TEAM_DOMAIN="swarm.hivetalk.org"
    BLOSSOM_ENABLED="true"
    BLOSSOM_PATH="blossom/"
    BLOSSOM_URL="http://localhost:3334"

    ```

## Compiling the Application

1. Clone the repository:

   ```bash
   git clone https://github.com/hivetalk/swarm.git
   cd swarm
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
