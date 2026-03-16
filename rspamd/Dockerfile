FROM rspamd/rspamd:nightly
# Seed map files into the image; Docker copies them into the
# named volume on first run.
COPY maps.d/ /etc/rspamd/maps.d/
