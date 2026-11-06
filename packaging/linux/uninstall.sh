#!/bin/sh
set -e
[ "$(id -u)" -eq 0 ] || exec sudo "$0" "$@"
/opt/myvpn/myvpnd uninstall || true
rm -rf /opt/myvpn /usr/share/applications/myvpn.desktop /usr/local/bin/myvpnd
echo "MyVpn desinstalado."
