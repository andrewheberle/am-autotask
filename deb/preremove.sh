#!/bin/sh
# Stop the service when the package is removed, but not on upgrade.
set -e

if [ -d /run/systemd/system ] && [ "$1" = "remove" ]; then
	systemctl stop am-autotask.service || true
fi
