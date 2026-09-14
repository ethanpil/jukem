#!/bin/sh
addgroup -S jukem 2>/dev/null
adduser -S -D -H -h /var/lib/jukem -s /sbin/nologin -G jukem -g jukem jukem 2>/dev/null
addgroup jukem audio 2>/dev/null
exit 0
