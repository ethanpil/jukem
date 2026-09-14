#!/bin/sh
# Restart the service so the new binary takes effect. Opt out in /etc/conf.d/jukem.
[ -f /etc/conf.d/jukem ] && . /etc/conf.d/jukem

case "${JUKEM_RESTART_ON_UPGRADE:-yes}" in
	[Yy]es|[Tt]rue|1) ;;
	*) echo " * jukem upgraded. Restart it with: rc-service jukem restart"; exit 0 ;;
esac

if command -v rc-service >/dev/null 2>&1 && rc-service jukem status >/dev/null 2>&1; then
	echo " * Restarting jukem"
	rc-service jukem restart
fi
exit 0
