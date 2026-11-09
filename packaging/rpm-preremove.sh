#!/bin/sh
# Used by the GoReleaser RPM. On uninstall (not upgrade), stop and disable
# silo's own unit. The incus units are left alone.
if [ "$1" -eq 0 ] 2>/dev/null; then
    systemctl --no-reload disable --now silo-incus-docker-fix.service >/dev/null 2>&1 || :
fi
