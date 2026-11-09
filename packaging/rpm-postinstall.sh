#!/bin/sh
# Used by the GoReleaser RPM (silo.spec does the same with systemd macros).
# On first install, enable the units listed in 80-silo.preset.
systemctl daemon-reload >/dev/null 2>&1 || :
if [ "$1" -eq 1 ] 2>/dev/null; then
    systemctl preset incus.socket incus-user.socket incus-startup.service \
        silo-incus-docker-fix.service >/dev/null 2>&1 || :
fi
