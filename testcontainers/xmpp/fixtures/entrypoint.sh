#!/bin/bash
set -euo pipefail

mkdir -p /var/lib/prosody /var/run/prosody /etc/prosody/certs
chown -R prosody:prosody /var/lib/prosody /var/run/prosody /etc/prosody || true

# Generate self-signed certs when missing (StartTLS still offered to clients).
if [[ ! -f /etc/prosody/certs/localhost.crt ]]; then
  printf '\n\n\n\n\n\n\n' | prosodyctl --root cert generate localhost || true
fi
if [[ ! -f /etc/prosody/certs/conference.localhost.crt ]]; then
  printf '\n\n\n\n\n\n\n' | prosodyctl --root cert generate conference.localhost || true
fi

prosodyctl --root register bridge localhost bridgepass >/dev/null 2>&1 || true
prosodyctl --root register occupant localhost occupantpass >/dev/null 2>&1 || true

chown -R prosody:prosody /var/lib/prosody /etc/prosody/certs /var/run/prosody || true

# Prosody refuses to run as root; drop privileges after setup.
if command -v runuser >/dev/null 2>&1; then
  exec runuser -u prosody -- prosody --config /etc/prosody/prosody.cfg.lua
fi
if command -v su-exec >/dev/null 2>&1; then
  exec su-exec prosody prosody --config /etc/prosody/prosody.cfg.lua
fi
exec su -s /bin/sh prosody -c 'prosody --config /etc/prosody/prosody.cfg.lua'
