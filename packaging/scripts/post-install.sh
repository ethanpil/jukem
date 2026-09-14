#!/bin/sh
[ -d /var/lib/jukem ] || install -d -o jukem -g jukem -m 0750 /var/lib/jukem
[ -d /var/log/jukem ] || install -d -o jukem -g jukem -m 0750 /var/log/jukem
[ -d /srv/jukem/music ] || install -d -o jukem -g jukem -m 0755 /srv/jukem/music
cat <<MSG
 *
 * jukem is installed. To start it now and at every boot:
 *   rc-update add jukem default
 *   rc-service jukem start
 * Then open http://<this-host>/ and follow the setup wizard.
 *
MSG
exit 0
