# Mattermost XMPP Bridge Plugin

[![Build Status](https://github.com/mattermost/mattermost-plugin-bridge-xmpp/actions/workflows/ci.yml/badge.svg)](https://github.com/mattermost/mattermost-plugin-bridge-xmpp/actions/workflows/ci.yml)

This plugin provides bidirectional message synchronization between Mattermost and XMPP servers, enabling seamless communication across both platforms.

## Features

- Bidirectional message synchronization (Mattermost ↔ XMPP)
- XMPP Multi-User Chat (MUC) support
- Configurable username prefixes for XMPP users
- Ghost user management for cross-platform user representation on the XMPP server with connection lifecycle management (**XEP-0077 only**)
- Comprehensive XMPP client with SASL Plain authentication

To learn more about plugins, see [our plugin documentation](https://developers.mattermost.com/extend/plugins/).

This template requires node v16 and npm v8. You can download and install nvm to manage your node versions by following the instructions [here](https://github.com/nvm-sh/nvm). Once you've setup the project simply run `nvm i` within the root folder to use the suggested version of node.

## Getting Started
Use GitHub's template feature to make a copy of this repository by clicking the "Use this template" button.

Alternatively shallow clone the repository matching your plugin name:
```
git clone --depth 1 https://github.com/mattermost/mattermost-plugin-bridge-xmpp com.example.bridge-xmpp
```

Note that this project uses [Go modules](https://github.com/golang/go/wiki/Modules). Be sure to locate the project outside of `$GOPATH`.

Edit the following files:
1. `plugin.json` with your `id`, `name`, and `description`:
```json
{
    "id": "com.example.bridge-xmpp",
    "name": "My Plugin",
    "description": "A plugin to enhance Mattermost."
}
```

2. `go.mod` with your Go module path, following the `<hosting-site>/<repository>/<module>` convention:
```
module github.com/example/my-plugin
```

3. `.golangci.yml` with your Go module path:
```yml
linters-settings:
  # [...]
  goimports:
    local-prefixes: github.com/example/my-plugin
```

Build your plugin:
```
make
```

This will produce a single plugin file (with support for multiple architectures) for upload to your Mattermost server:

```
dist/com.example.bridge-xmpp.tar.gz
```

## Development

To avoid having to manually install your plugin, build and deploy your plugin using one of the following options. In order for the below options to work, you must first enable plugin uploads via your config.json or API and restart Mattermost.

```json
    "PluginSettings" : {
        ...
        "EnableUploads" : true
    }
```

### Development guidance

1. Fewer packages is better: default to the main package unless there's good reason for a new package.

2. Coupling implies same package: don't jump through hoops to break apart code that's naturally coupled.

3. New package for a new interface: a classic example is the sqlstore with layers for monitoring performance, caching and mocking.

4. New package for upstream integration: a discrete client package for interfacing with a 3rd party is often a great place to break out into a new package

### Modifying the server boilerplate

The server code comes with some boilerplate for creating an api, using slash commands, accessing the kvstore and using the cluster package for jobs.

#### Api

api.go implements the ServeHTTP hook which allows the plugin to implement the http.Handler interface. Requests destined for the `/plugins/{id}` path will be routed to the plugin. This file also contains a sample `HelloWorld` endpoint that is tested in plugin_test.go.

#### Command package

This package contains the boilerplate for adding a slash command and an instance of it is created in the `OnActivate` hook in plugin.go. If you don't need it you can delete the package and remove any reference to `commandClient` in plugin.go. The package also contains an example of how to create a mock for testing.

#### KVStore package

This is a central place for you to access the KVStore methods that are available in the `pluginapi.Client`. The package contains an interface for you to define your methods that will wrap the KVStore methods. An instance of the KVStore is created in the `OnActivate` hook.

### Deploying with Local Mode

If your Mattermost server is running locally, you can enable [local mode](https://docs.mattermost.com/administration/mmctl-cli-tool.html#local-mode) to streamline deploying your plugin. Edit your server configuration as follows:

```json
{
    "ServiceSettings": {
        ...
        "EnableLocalMode": true,
        "LocalModeSocketLocation": "/var/tmp/mattermost_local.socket"
    },
}
```

and then deploy your plugin:
```
make deploy
```

You may also customize the Unix socket path:
```bash
export MM_LOCALSOCKETPATH=/var/tmp/alternate_local.socket
make deploy
```

If developing a plugin with a webapp, watch for changes and deploy those automatically:
```bash
export MM_SERVICESETTINGS_SITEURL=http://localhost:8065
export MM_ADMIN_TOKEN=j44acwd8obn78cdcx7koid4jkr
make watch
```

## XMPP Client Doctor

The plugin includes a diagnostic tool to test XMPP client connectivity:

```bash
go run cmd/xmpp-client-doctor/main.go [flags]
```

### Usage

Test connectivity with default development server settings:
```bash
go run cmd/xmpp-client-doctor/main.go
```

Test with custom XMPP server:
```bash
go run cmd/xmpp-client-doctor/main.go \
  -server="xmpp.example.com:5222" \
  -username="myuser@example.com" \
  -password="mypassword" \
  -resource="test"
```

### Flags

- `-server`: XMPP server address (default: `localhost:5222`)
- `-username`: XMPP username/JID (default: `testuser@localhost`)
- `-password`: XMPP password (default: `testpass`)
- `-resource`: XMPP resource (default: `doctor`)
- `-verbose`: Enable verbose logging (default: `true`)
- `-insecure-skip-verify`: Skip TLS certificate verification for development (default: `true`)

### Development Server

The tool defaults are configured for the development XMPP server in `./sidecar/`. To start the development server:

```bash
cd sidecar
docker-compose up -d
```

The development server runs Openfire XMPP server with:
- XMPP client connections on port 5222
- Admin console on http://localhost:9090
- Default test credentials: `testuser@localhost` / `testpass`

### Deploying with credentials

Alternatively, you can authenticate with the server's API with credentials:
```bash
export MM_SERVICESETTINGS_SITEURL=http://localhost:8065
export MM_ADMIN_USERNAME=admin
export MM_ADMIN_PASSWORD=password
make deploy
```

or with a [personal access token](https://docs.mattermost.com/developer/personal-access-tokens.html):
```bash
export MM_SERVICESETTINGS_SITEURL=http://localhost:8065
export MM_ADMIN_TOKEN=j44acwd8obn78cdcx7koid4jkr
make deploy
```

### Releasing new versions

The version of a plugin is determined at compile time, automatically populating a `version` field in the [plugin manifest](plugin.json):
* If the current commit matches a tag, the version will match after stripping any leading `v`, e.g. `1.3.1`.
* Otherwise, the version will combine the nearest tag with `git rev-parse --short HEAD`, e.g. `1.3.1+d06e53e1`.
* If there is no version tag, an empty version will be combined with the short hash, e.g. `0.0.0+76081421`.

To disable this behaviour, manually populate and maintain the `version` field.

## How to Release

To trigger a release, follow these steps:

1. **For Patch Release:** Run the following command:
    ```
    make patch
    ```
   This will release a patch change.

2. **For Minor Release:** Run the following command:
    ```
    make minor
    ```
   This will release a minor change.

3. **For Major Release:** Run the following command:
    ```
    make major
    ```
   This will release a major change.

4. **For Patch Release Candidate (RC):** Run the following command:
    ```
    make patch-rc
    ```
   This will release a patch release candidate.

5. **For Minor Release Candidate (RC):** Run the following command:
    ```
    make minor-rc
    ```
   This will release a minor release candidate.

6. **For Major Release Candidate (RC):** Run the following command:
    ```
    make major-rc
    ```
   This will release a major release candidate.

## Q&A

### How do I make a server-only or web app-only plugin?

Simply delete the `server` or `webapp` folders and remove the corresponding sections from `plugin.json`. The build scripts will skip the missing portions automatically.

### How do I include assets in the plugin bundle?

Place them into the `assets` directory. To use an asset at runtime, build the path to your asset and open as a regular file:

```go
bundlePath, err := p.API.GetBundlePath()
if err != nil {
    return errors.Wrap(err, "failed to get bundle path")
}

profileImage, err := ioutil.ReadFile(filepath.Join(bundlePath, "assets", "profile_image.png"))
if err != nil {
    return errors.Wrap(err, "failed to read profile image")
}

if appErr := p.API.SetProfileImage(userID, profileImage); appErr != nil {
    return errors.Wrap(err, "failed to set profile image")
}
```

### How do I build the plugin with unminified JavaScript?
Setting the `MM_DEBUG` environment variable will invoke the debug builds. The simplist way to do this is to simply include this variable in your calls to `make` (e.g. `make dist MM_DEBUG=1`).
