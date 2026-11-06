#!/bin/sh
# Integration test of the full data path on Linux (TUN + routes + DNS + PPP).
# Run inside a container with NET_ADMIN and /dev/net/tun (see scripts/test-linux-docker.sh).
set -eu
/out/fakegw -sso-port 8020 > /tmp/gw.txt 2>/tmp/gw.log &
sleep 1
ADDR=$(cut -d' ' -f1 /tmp/gw.txt); FP=$(cut -d' ' -f2 /tmp/gw.txt)
PORT=${ADDR##*:}
echo "fake gateway en $ADDR"
/out/myvpnd connect -foreground -host 127.0.0.1 -port "$PORT" -trust-cert "$FP" -cookie abc123cookie > /tmp/vpn.log 2>&1 &
VPN=$!
for i in $(seq 1 30); do grep -q Conectado /tmp/vpn.log && break; sleep 0.5; done
cat /tmp/vpn.log
echo "--- interfaz"; ip addr show myvpn0
echo "--- rutas"; ip route | grep myvpn0
echo "--- DNS"; cat /etc/resolv.conf | head -5
echo "--- ping a través del túnel (10.0.0.5, respondido por el gateway falso)"
ping -c 3 -W 2 10.0.0.5
echo "--- desconexión"
kill -INT $VPN; sleep 2
if ip link show myvpn0 >/dev/null 2>&1; then echo "FALLO: la interfaz sigue existiendo"; exit 1; fi
if grep -q "generado por MyVpn" /etc/resolv.conf; then echo "FALLO: DNS no restaurado"; exit 1; fi
echo "INTEGRACION_LINUX_OK"
