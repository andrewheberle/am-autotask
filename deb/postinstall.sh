#!/bin/sh
# The service is not enabled or started on install, as it needs
# /etc/am-autotask/config.yaml and its credentials first. On upgrade a
# running service is restarted to pick up the new binary.
set -e

if [ -d /run/systemd/system ]; then
	systemctl daemon-reload || true
	if [ "$1" = "configure" ] && [ -n "$2" ]; then
		systemctl try-restart am-autotask.service || true
	fi
fi
