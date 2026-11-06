# Firma localmente los ejecutables de Windows de MyVpn con un certificado de
# firma de código instalado en el equipo (token USB o Certum SimplySign).
#
# Uso (PowerShell en Windows, con el Windows SDK instalado):
#   .\scripts\sign-windows.ps1 -Thumbprint <huella SHA1 del certificado> -Zip MyVpn-1.1.0-windows-amd64.zip,MyVpn-1.1.0-windows-arm64.zip
#
# Deja los .zip firmados con el mismo nombre en .\signed\ (listos para subir a
# la Release con: gh release upload vX.Y.Z signed\*.zip --clobber).
param(
  [Parameter(Mandatory)] [string] $Thumbprint,
  [Parameter(Mandatory)] [string[]] $Zip,
  [string] $TimestampUrl = "http://time.certum.pl"
)
$ErrorActionPreference = "Stop"

$signtool = Get-ChildItem "${env:ProgramFiles(x86)}\Windows Kits\10\bin\*\x64\signtool.exe" -ErrorAction SilentlyContinue |
  Sort-Object FullName -Descending | Select-Object -First 1
if (-not $signtool) { throw "No se encontró signtool.exe: instala el Windows SDK (componente 'Signing Tools')." }

$out = Join-Path (Get-Location) "signed"
New-Item -ItemType Directory -Force $out | Out-Null
foreach ($z in $Zip) {
  $name = [IO.Path]::GetFileNameWithoutExtension($z)
  $stage = Join-Path $env:TEMP "myvpn-sign\$name"
  Remove-Item -Recurse -Force $stage -ErrorAction SilentlyContinue
  Expand-Archive $z -DestinationPath $stage
  $exes = Get-ChildItem $stage -Filter *.exe
  & $signtool.FullName sign /sha1 $Thumbprint /fd sha256 /tr $TimestampUrl /td sha256 /d "MyVpn" $exes.FullName
  if ($LASTEXITCODE) { throw "signtool falló" }
  foreach ($e in $exes) {
    $sig = Get-AuthenticodeSignature $e.FullName
    if ($sig.Status -ne "Valid") { throw "Firma inválida en $($e.Name): $($sig.Status)" }
    "{0}: {1}" -f $e.Name, $sig.SignerCertificate.Subject
  }
  $dest = Join-Path $out "$name.zip"
  Remove-Item $dest -ErrorAction SilentlyContinue
  Compress-Archive -Path "$stage\*" -DestinationPath $dest
  "Firmado: $dest"
}
