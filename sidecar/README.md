# Development XMPP Server

This folder contains a `docker-compose.yml` file for development purposes. It sets up a local XMPP server (Openfire) to use while developing the Mattermost XMPP bridge plugin.

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

Open your web browser and go to: http://localhost:9090

### 2. Complete Setup Wizard

1. **Language Selection**: Choose your preferred language
2. **Server Settings**: 
   - Server Domain: `localhost` (default is fine)
   - Keep other defaults
3. **Database Settings**: 
   - Choose "Embedded Database" for development
   - This creates a local database that persists in Docker volumes
4. **Profile Settings**: 
   - Choose "Default" (no LDAP needed for development)
5. **Administrator Account**:
   - Username: `admin`
   - Password: `admin` (for development consistency)
   - Email: `admin@localhost`

### 3. Create Test User

After completing the setup wizard:

1. Log in to the admin console with `admin` / `admin`
2. Go to **Users/Groups** → **Create New User**
3. Fill in the user details:
   - **Username**: `testuser`
   - **Password**: `testpass`
   - **Confirm Password**: `testpass`
   - **Name**: `Test User`
   - **Email**: `testuser@localhost`
4. Click **Create User**

### 4. Test Connectivity

Run the doctor tool to verify everything is working:

```bash
make devserver_doctor
```

You should see successful connection, ping, and disconnect messages.

## Server Details

- **Admin Console**: http://localhost:9090
- **XMPP Server**: localhost:5222 (client connections)
- **XMPP SSL Server**: localhost:5223 (SSL client connections)
- **XMPP Server-to-Server**: localhost:5269
- **File Transfer Proxy**: localhost:7777

## Test Credentials

After setup, use these credentials for testing:

- **Admin User**: `admin` / `admin`
- **Test User**: `testuser@localhost` / `testpass`

## Data Persistence

The server data is stored in Docker volumes:
- `sidecar_openfire_data`: Openfire configuration and database
- `sidecar_postgres_data`: PostgreSQL database (if you choose PostgreSQL instead of embedded DB)

## Troubleshooting

### Server Won't Start
```bash
# Check if ports are already in use
lsof -i :9090
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
  -username="testuser@localhost" \
  -password="testpass" \
  -insecure-skip-verify=true \
  -verbose=true
```

## Development Notes

- The server uses self-signed certificates, so the doctor tool defaults to `-insecure-skip-verify=true`
- All data persists between container restarts unless you run `make devserver_clean`
- The PostgreSQL and Adminer services are included but optional (you can use embedded database)
- The server takes ~30 seconds to fully start up after `docker compose up`