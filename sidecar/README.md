# Development XMPP Server

This folder contains a `docker-compose.yml` file for development purposes. It sets up a local XMPP server (Openfire) plus a browser-based XMPP client ([Converse.js](https://conversejs.org/)) to use while developing the Mattermost XMPP bridge plugin.

## Quick Start

From the project root directory, use the Makefile targets:

```bash
# Start the XMPP server
make devserver_start

# Check server status
make devserver_status

# Test connectivity with the doctor tool
make devserver_doctor

# Stop the server
make devserver_stop
```

## Manual Docker Usage

Alternatively, you can manage the server directly:

```bash
# Start the server
cd sidecar
docker compose up -d

# Stop the server
docker compose down

# View logs
docker compose logs -f openfire
```

## Initial Setup

After starting the server for the first time, you need to complete the Openfire setup:

### 1. Access Admin Console

Open your web browser and go to: [http://localhost:19090](http://localhost:19090)

### 2. Complete Setup Wizard

1. **Language Selection**: Choose your preferred language
2. **Server Settings**:
   - Server Domain and Server Host Name (FQDN): `localhost`
   - **Uncheck "Restrict admin console to localhost"**. It is checked by default and binds the console to `127.0.0.1` *inside* the container, making [http://localhost:19090](http://localhost:19090) unreachable from the host
   - Keep other defaults
3. **Database Settings**:
   - Choose "Embedded Database" for development
   - This creates a local database that persists in Docker volumes
4. **Profile Settings**:
   - Choose "Default" (no LDAP needed for development)
5. **Administrator Account**:
   - Email: `admin@localhost`
   - Password: `admin` (for development consistency)
6. When finishing setup the server will be non-responsive for a minute

### 3. Create the bridge and test users

You need **two separate accounts**: one for the bridge itself, and one to act as a
human XMPP participant. Do not use a single account for both; see
[Why two accounts](#why-two-accounts) below.

After completing the setup wizard, log in to the admin console with `admin` / `admin`,
then for each user go to **Users/Groups** → **Create New User** and click **Create User**:


| Field                | Bridge account     | Human test account |
| -------------------- | ------------------ | ------------------ |
| **Username**         | `bridge`           | `test`             |
| **Password**         | `bridgepass`       | `testpass`         |
| **Confirm Password** | `bridgepass`       | `testpass`         |
| **Name**             | `Bridge Bot`       | `Test User`        |
| **Email**            | `bridge@localhost` | `test@localhost`   |


`bridge@localhost` goes in the plugin settings. `test@localhost` is the one you log
into the web XMPP client with.

#### Why two accounts

The bridge joins each MUC room using the localpart of its own JID as its nickname, so
`bridge@localhost` appears in rooms as `bridge`. Incoming messages are matched against
that nickname to drop the bridge's own echo and avoid a message loop.

If you sign in to the XMPP client with the *same* account the plugin uses, your session
joins under the same nickname, and **every message you send is silently discarded as the
bridge's own echo**. It never reaches Mattermost, and nothing is logged. Openfire
permits this because both sessions share one bare JID, so you do not even get a nickname
conflict to warn you.

For the same reason, do not give a human account a username starting with the ghost user
prefix (`mm_` by default), because those nicknames are also treated as bridge-owned.

### 4. Create Test MUC Room

For testing Multi-User Chat functionality, create a test room:

1. In the admin console, go to **Group Chat** → **Create New Room**
2. Fill in the room details:
   - **Room ID**: `test1`
   - **Room Name**: `Test Room 1`
   - **Description**: `Test room for XMPP bridge development`
3. Leave rest as defaults.
4. Click **Save changes**

The room will be accessible as `test1@conference.localhost` for testing MUC operations.

### 5. Test Connectivity

Run the doctor tool to verify everything is working:

```bash
make devserver_doctor
```

You should see successful connection, ping, and disconnect messages.

#### Test MUC Operations

To test Multi-User Chat room operations (requires the test room created above):

```bash
# Test MUC room join/leave operations
go run cmd/xmpp-client-doctor/main.go --test-muc
```

This will test joining the `test1@conference.localhost` room, waiting 5 seconds, and then leaving.

## Connecting as an XMPP User

The `converse` service is an `nginx:alpine` container serving `converse/index.html`, which
loads Converse.js from `cdn.conversejs.org` (so the container needs no build step, but the
page does need internet access). The same nginx proxies `/xmpp-websocket` to
`http://openfire:7070/ws/`, keeping the browser on one origin, so Openfire needs no extra
published ports and no CORS setup:

1. Open [http://localhost:8080](http://localhost:8080)
2. **JID**: `test` (the `localhost` domain is prefilled), **Password**: `testpass`
 (use the human test account here, never the bridge account)
3. Join the test room as `test1@conference.localhost`

Useful for watching messages arrive from the bridge in real time, and for sending
messages to a Mattermost-bridged channel as a real XMPP user.

## Server Details

- **Admin Console**: [http://localhost:19090](http://localhost:19090)
- **Web XMPP Client**: [http://localhost:8080](http://localhost:8080)
- **XMPP Server**: localhost:5222 (client connections)
- **XMPP SSL Server**: localhost:5223 (SSL client connections)
- **XMPP Server-to-Server**: localhost:5269
- **File Transfer Proxy**: localhost:7777

## Test Credentials

After setup, use these credentials for testing:

- **Admin User**: `admin` / `admin` (admin console, and the doctor tool's default)
- **Bridge Account**: `bridge@localhost` / `bridgepass` (plugin settings only)
- **Human Test User**: `test@localhost` / `testpass` (web XMPP client only)

## Connecting the bridge plugin to the XMPP server

`XMPP Username` / `XMPP Password` are the **bridge bot** account. That client connects, joins mapped MUC rooms, receives XMPP traffic, registers ghost users (when enabled), and (when ghost users are off) sends Mattermost messages into XMPP as itself. Use `bridge@localhost`, not Openfire `admin`, and not the `test@localhost` account you sign in to the XMPP client with.

### 1. Plugin settings (required)

After Openfire is set up and the plugin is deployed to Mattermost:

1. Go to **System Console → Plugins → Mattermost Bridge for XMPP**
2. Set:
   - **XMPP Server URL**: `localhost:5222`
   - **XMPP Username**: `bridge@localhost`
   - **XMPP Password**: `bridgepass`
   - **Skip TLS Certificate Verification**: enabled (self-signed certs)
   - **Enable Message Synchronization**: enabled
3. Save and ensure the plugin is enabled

### 2. Ghost users (recommended for local development)

Ghost users make each Mattermost user appear as a real XMPP account (`mm_{userID}@localhost`) instead of everything posting as the bridge bot.

**Openfire (enable XEP-0077):**

> This should be enabled by default, but in order to use ghost users we need to make sure.

1. Open [http://localhost:19090](http://localhost:19090) and log in as `admin` / `admin`
2. Go to **Server → Server Settings → Registration &amp; Login**
3. Enable **Inband Account Registration** (and save)
4. Verify with the doctor (registers/cancels a temporary ghost):

```
make devserver_doctor
```

**Plugin:**

Still keep `bridge@localhost` / `bridgepass` as the bridge bot, then set:

- **Enable XMPP Ghost Users**: enabled
- **XMPP Ghost User Prefix**: `mm_`
- **XMPP Ghost User Domain**: leave empty (defaults to `localhost` from the bridge JID)
- **Enable Ghost User Cleanup**: enabled (removes ghost accounts when MM users are deleted)

If IBR is disabled or unsupported, the plugin falls back to sending as the bridge bot with a `<username>` prefix.


| Direction             | Without ghost users                                 | With ghost users                                   |
| --------------------- | --------------------------------------------------- | -------------------------------------------------- |
| **Mattermost → XMPP** | Bridge bot posts; body prefixed with `<mmUsername>` | Per-user XMPP account via XEP-0077 posts as itself |
| **XMPP → Mattermost** | Shared Channels remote user (`xmpp-{nickname}`)     | Same (ghost setting only affects MM→XMPP)          |


### 3. Map a channel

In a Mattermost channel (as a system admin):

```
/xmppbridge map test1@conference.localhost
```

Check connection:

```
/xmppbridge status
```

## Data Persistence

## Data Persistence

The server data is stored in Docker volumes:

- `sidecar_openfire_data`: Openfire configuration and database
- `sidecar_postgres_data`: PostgreSQL database (if you choose PostgreSQL instead of embedded DB)

## Troubleshooting

### Server Won't Start

```bash
# Check if ports are already in use
lsof -i :19090
lsof -i :5222

# View server logs
make devserver_logs
```

### Reset Everything

```bash
# This removes all data and containers
make devserver_clean
```

### Test Different Configurations

```bash
# Test with custom server settings
go run cmd/xmpp-client-doctor/main.go \
  -server="localhost:5222" \
  -username="bridge@localhost" \
  -password="bridgepass" \
  -insecure-skip-verify=true \
  -verbose=true
```

## Development Notes

- The server uses self-signed certificates, so the doctor tool defaults to `-insecure-skip-verify=true`
- All data persists between container restarts unless you run `make devserver_clean`
- The PostgreSQL and Adminer services are included but optional (you can use embedded database)
- The web client is configured in `converse/index.html` via the `converse.initialize({...})`
call; edit it and reload the page, no container rebuild needed
- `discover_connection_methods: false` is required there: Openfire publishes no XEP-0156
records, and without it Converse probes for them and ignores `websocket_url`
- Converse 14 publishes an ES module, so `index.html` must use `<script type="module">`
and `import converse from ...`; a plain `<script src>` fails with `Cannot use
'import.meta' outside a module` followed by `converse is not defined`
- Pin the Converse version in the two `cdn.conversejs.org` URLs in `converse/index.html`
- `converse/sw.js` unregisters the service worker left behind by the old `xmpp-web`
client; keep it, and do not add an SPA `try_files` fallback to `index.html` or requests
for missing `.js` files will be answered with HTML
- The proxy returns `502` on the WebSocket path until Openfire finishes booting
- The server takes \~30 seconds to fully start up after `docker compose up`

