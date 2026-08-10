FROM prosody/prosody:0.11.9

USER root

COPY prosody.cfg.lua /etc/prosody/prosody.cfg.lua
COPY entrypoint.sh /usr/local/bin/e2e-entrypoint.sh

RUN chmod +x /usr/local/bin/e2e-entrypoint.sh \
    && mkdir -p /var/lib/prosody /var/run/prosody /etc/prosody/certs \
    && chown -R prosody:prosody /etc/prosody /var/lib/prosody /var/run/prosody

ENTRYPOINT ["/usr/local/bin/e2e-entrypoint.sh"]
