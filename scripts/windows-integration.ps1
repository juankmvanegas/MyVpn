# Windows integration test (run as Administrator, e.g. on a GitHub Actions runner):
# installs the MyVpn service, connects to the emulated FortiGate, checks the
# Wintun adapter, routes and a ping through the tunnel, then uninstalls.
param([string]$Zip = (Get-ChildItem dist\MyVpn-*-windows-amd64.zip | Select-Object -First 1).FullName)
$ErrorActionPreference = "Stop"
$it = Join-Path $env:TEMP "myvpn-it"
Remove-Item -Recurse -Force $it -ErrorAction SilentlyContinue
Expand-Archive $Zip -DestinationPath $it
go build -o "$it\fakegw.exe" ./cmd/fakegw

$gw = Start-Process "$it\fakegw.exe" -ArgumentList "-sso-port", "8020" -RedirectStandardOutput "$it\gw.txt" -RedirectStandardError "$it\gw.err" -PassThru -WindowStyle Hidden
Start-Sleep 2
$parts = (Get-Content "$it\gw.txt" -First 1).Split(" ")
$port = $parts[0].Split(":")[-1]; $fp = $parts[1]
Write-Host "fake gateway en puerto $port"

$d = "$it\myvpnd.exe"
try {
  & $d install; if ($LASTEXITCODE) { throw "install falló" }
  Start-Sleep 2
  & $d connect -host 127.0.0.1 -port $port -trust-cert $fp -cookie abc123cookie
  if ($LASTEXITCODE) { & $d logs; throw "connect falló" }
  Get-NetIPAddress -InterfaceAlias MyVpn -AddressFamily IPv4 | Format-Table IPAddress, PrefixLength
  Get-NetRoute -InterfaceAlias MyVpn -AddressFamily IPv4 | Format-Table DestinationPrefix, NextHop
  Get-DnsClientServerAddress -InterfaceAlias MyVpn -AddressFamily IPv4 | Format-Table ServerAddresses
  if (-not (Test-Connection 10.0.0.5 -Count 3 -Quiet)) { & $d logs; throw "ping a través del túnel falló" }
  Write-Host "ping OK a través del túnel"
  & $d disconnect
  Start-Sleep 2
  if (Get-NetAdapter -Name MyVpn -ErrorAction SilentlyContinue) { throw "el adaptador MyVpn sigue existiendo" }
  Write-Host "INTEGRACION_WINDOWS_OK"
} finally {
  & $d logs
  & $d uninstall
  Stop-Process $gw -ErrorAction SilentlyContinue
}
