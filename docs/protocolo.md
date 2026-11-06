# Protocolo FortiGate SSL VPN + SAML

Cómo funciona la conexión que implementa MyVpn. Se determinó analizando un FortiClient 7.4
instalado (con [REA](https://github.com/morluto/rea) y herramientas estáticas) y verificándolo
contra un FortiGate real. Se distingue lo **observado** (presente en los binarios, la
configuración o el tráfico del gateway) de lo **inferido**.

## Artefactos analizados (FortiClient 7.4, macOS)

| Componente | Qué aporta |
|---|---|
| UI Electron (`FortiClient.app/Contents/Resources/app.asar`) | Flujo SAML, puerto SSO 8020, captura de `SVPNCOOKIE` |
| `FortiTray.app` | Endpoints `/remote/*`, User-Agent |
| Extensión de red `com.fortinet.forticlient.macos.vpn.nwextension` | Petición del túnel, PPP (`PPPProtocol.swift`), MTU |
| Perfil `conf/vpn.plist` y logs del proveedor VPN | Opciones SSO, MTU 1300, DNS dividido |

## Flujo

1. **Info** (observado): `GET /remote/info` → `<info><api … sso_port='8020' …/></info>`.
2. **SAML con navegador externo** (observado): `GET /remote/saml/start?redirect=1` en el navegador
   del usuario; el gateway redirige al proveedor de identidad y, al terminar, a
   `http://127.0.0.1:<sso_port>/?id=<id>`. La UI de FortiClient define `SAML_DEFAULT_SSO_PORT=8020`.
3. **Intercambio del id** (observado): `GET /remote/saml/auth_id?id=<id>` → `Set-Cookie: SVPNCOOKIE=…`.
4. **Asignación y configuración** (observado): `GET /remote/fortisslvpn` y
   `GET /remote/fortisslvpn_xml`, que devuelve la IP (`assigned-addr`), DNS (`dns`, `dns-suffix`,
   `split-dns`), rutas divididas (`split-tunnel-info`, con `negate='1'` para exclusión) y tiempos
   de sesión. Sin `split-tunnel-info` el túnel es completo.
5. **Túnel** (observado): sobre una conexión TLS nueva
   `GET /remote/sslvpn-tunnel HTTP/1.1`, `Host: sslvpn`, `Cookie: SVPNCOOKIE=…`.
   No hay respuesta HTTP: empieza PPP directamente. Si la cookie no es válida llega `HTTP/1.1 403`.
6. **Encapsulado** (observado): cabecera de 6 bytes `[longitud total][0x5050][longitud PPP]`
   seguida de la trama PPP (`ff 03` + protocolo de 2 bytes), sin HDLC ni FCS.
7. **PPP** (observado): LCP (MRU, Magic-Number, Echo como keepalive) e IPCP (dirección IPv4 y
   DNS primario/secundario). Otros protocolos (IPv6CP, CCP) se rechazan con Protocol-Reject.
8. **Red** (observado en FortiClient): MTU 1300; IPv6 bloqueado en VPN IPv4; en túnel completo el
   DNS de la VPN se usa para todo y además se instala un resolvedor por cada dominio de `split-dns`.

## Decisiones de MyVpn

- Siempre navegador externo (más simple y portable que el WebView embebido de FortiClient).
- Solo IPv4, como FortiClient en este modo.
- macOS, túnel completo: MyVpn se publica en `SystemConfiguration` como servicio de red principal
  (`OverridePrimary`) ligado a la interfaz `utun`, para que las consultas DNS salgan por el túnel.
  Sobrescribir el DNS del servicio Wi-Fi no sirve: macOS envía esas consultas por la interfaz física
  y nunca alcanzan los DNS internos (comprobado en pruebas reales).
- Túnel dividido: solo los dominios internos usan el DNS de la VPN (resolvedor suplementario en macOS,
  `resolvectl domain` en Linux).
