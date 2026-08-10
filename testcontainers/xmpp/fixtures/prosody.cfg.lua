-- Prosody configuration for Mattermost XMPP bridge e2e tests.
admins = { "bridge@localhost" }

daemonize = false
pidfile = "/var/run/prosody/prosody.pid"

data_path = "/var/lib/prosody"

modules_enabled = {
    "roster";
    "saslauth";
    "tls";
    "disco";
    "carbons";
    "pep";
    "private";
    "blocklist";
    "vcard4";
    "vcard_legacy";
    "version";
    "uptime";
    "time";
    "ping";
    "register";
    "posix";
}

modules_disabled = {
    "s2s";
}

allow_registration = false
authentication = "internal_hashed"

c2s_require_encryption = false
s2s_secure_auth = false

consider_bosh_secure = true

certificates = "certs"

log = {
    { levels = { min = "info" }, to = "console" };
}

VirtualHost "localhost"
    enabled = true

Component "conference.localhost" "muc"
    name = "E2E chatrooms"
    -- Allow any local user to create rooms (Prosody 0.11).
    restrict_room_creation = "local"
    max_history_messages = 20
    muc_room_default_persistent = true
    muc_room_locking = false
    muc_tombstones = false
