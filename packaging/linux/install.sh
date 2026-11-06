#!/bin/sh
# Instala MyVpn en Linux: aplicación en /opt/myvpn, acceso directo y servicio systemd.
set -e
[ "$(id -u)" -eq 0 ] || exec sudo "$0" "$@"
DIR=$(cd "$(dirname "$0")" && pwd)
OWNER=${SUDO_UID:-${PKEXEC_UID:-0}}
install -d /opt/myvpn
install -m 755 "$DIR/MyVpn" "$DIR/MyVpnTray" "$DIR/myvpnd" /opt/myvpn/
install -m 644 "$DIR/appicon.png" /opt/myvpn/myvpn.png
install -m 644 "$DIR/myvpn.desktop" /usr/share/applications/myvpn.desktop
ln -sf /opt/myvpn/myvpnd /usr/local/bin/myvpnd
/opt/myvpn/myvpnd install --owner-uid "$OWNER"
echo "MyVpn instalado. Ábrelo desde el menú de aplicaciones o con: /opt/myvpn/MyVpn"
