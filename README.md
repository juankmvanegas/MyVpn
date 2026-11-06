# MyVpn

Cliente SSL VPN ligero para **FortiGate** con inicio de sesión único (**SAML**, p. ej. Azure AD / Entra ID).
Alternativa a FortiClient cuando solo necesitas la VPN: sin antivirus, filtro web, ZTNA ni
periodo de prueba. Funciona en **macOS (Apple Silicon e Intel)**, **Windows** y **Linux**.

Equivale a una conexión de FortiClient **SSL-VPN** con *Gateway remoto*, *Personalizar puerto* y
*Habilitar inicio de sesión único (SSO) para el túnel VPN*.

## Descargas

Instaladores en la página de **[Releases](../../releases/latest)**:

| Sistema | Archivo |
|---|---|
| macOS 10.13+ (Apple Silicon e Intel, universal) | `MyVpn-<versión>-macos-universal.dmg` |
| Windows 10/11 x64 | `MyVpn-<versión>-windows-amd64.zip` |
| Windows 11 ARM64 | `MyVpn-<versión>-windows-arm64.zip` |
| Linux x64 / ARM64 (GTK3 + WebKit2GTK 4.1) | `MyVpn-<versión>-linux-{amd64,arm64}.tar.gz` |

## Instalación

### macOS
1. Abre el `.dmg` y arrastra **MyVpn** a *Aplicaciones*.
2. La app no está firmada por un desarrollador de Apple. La primera vez: clic derecho → **Abrir**;
   si macOS lo bloquea, ve a *Ajustes del Sistema → Privacidad y seguridad → Abrir igualmente*,
   o ejecuta: `xattr -dr com.apple.quarantine /Applications/MyVpn.app`
3. Pulsa **Instalar** en el aviso amarillo (pide la contraseña del Mac una sola vez para instalar
   el servicio de red).

### Windows
1. Descomprime el `.zip` en una carpeta fija y abre `MyVpn.exe`.
2. Pulsa **Instalar** y acepta el aviso de administrador (UAC). Ver `LEEME.txt`.
3. Si Windows 11 muestra *"El Control inteligente de aplicaciones bloqueó una aplicación"*, esa
   versión no está firmada digitalmente: ver [docs/firma-windows.md](docs/firma-windows.md).

### Linux
```sh
tar xzf MyVpn-<versión>-linux-amd64.tar.gz && cd MyVpn-*/ && ./install.sh
```

## Uso
1. Crea una conexión con el botón **+**: nombre, *gateway remoto* y *puerto* que te indique tu
   organización (los mismos que usarías en FortiClient).
2. Pulsa **Conectar**: se abre el navegador para iniciar sesión (usuario, contraseña y doble factor).
3. Al cerrar la ventana la VPN sigue activa y el icono queda en la barra de menús (macOS) o junto
   al reloj (Windows/Linux), con *Conectar / Desconectar / Abrir MyVpn / Iniciar con el sistema / Salir*.

- Cuando el gateway expira la sesión, MyVpn pide iniciar sesión de nuevo; ante cortes de red reconecta solo.
- DNS: igual que FortiClient. En túnel completo todas las consultas van al DNS de la VPN; en túnel
  dividido, solo los dominios internos que publica el gateway.
- Línea de comandos: `myvpnd connect -profile NOMBRE`, `myvpnd status`, `myvpnd logs`, `myvpnd disconnect`.

## Arquitectura

| Proceso | Privilegios | Función |
|---|---|---|
| `MyVpn` (Wails) | usuario | Ventana, conexiones, inicio de sesión SSO en el navegador |
| `MyVpnTray` | usuario | Icono en la barra de menús / bandeja del sistema |
| `myvpnd` | root / SYSTEM (launchd, systemd, servicio de Windows) | Túnel TLS + PPP, interfaz TUN (utun / tun / Wintun), rutas y DNS |

La app y el servicio se comunican por HTTP sobre un socket Unix (`/var/run/myvpn.sock`, solo
accesible para tu usuario) o una tubería con nombre (`\\.\pipe\myvpn`). La cookie de sesión nunca se
guarda en disco. El protocolo está documentado en [docs/protocolo.md](docs/protocolo.md).

## Compilar
```sh
./scripts/build-macos.sh     # dist/*.dmg (universal arm64 + x86_64)
./scripts/build-windows.sh   # dist/*windows-{amd64,arm64}.zip (compilación cruzada)
./scripts/build-linux.sh     # dist/*linux-{arm64,amd64}.tar.gz (en Docker)
```
Requisitos: Go 1.26, [Wails](https://wails.io) CLI v2.17.1, Xcode Command Line Tools (macOS) y Docker (Linux).
El flujo `.github/workflows/build.yml` compila y prueba todo, y publica una Release al crear una etiqueta `v*`.

## Pruebas
- `go test ./internal/...`: gateway FortiGate emulado (SAML, XML, PPP con Nak/Ack, keepalive y tráfico).
- `scripts/test-linux-docker.sh`: interfaz TUN real en Linux, rutas, DNS, ping a través del túnel
  y verificación de que todo se revierte al desconectar.
- `scripts/windows-integration.ps1`: lo mismo con Wintun en Windows (se ejecuta en CI).

## Aviso
Proyecto independiente, no afiliado a Fortinet. FortiGate y FortiClient son marcas de Fortinet, Inc.
`wintun.dll` es software de WireGuard LLC, redistribuido sin modificar según su licencia.
