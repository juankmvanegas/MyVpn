# Firma de código para Windows

Windows 11 con **Control inteligente de aplicaciones** (Smart App Control) bloquea los programas
que no están firmados por un editor de confianza, sin opción de "Ejecutar de todas formas".
Para que MyVpn se pueda instalar en esos equipos, los `.exe` deben llevar una firma Authenticode
**RSA** emitida por una entidad de confianza de Microsoft (Smart App Control no acepta firmas ECC
ni certificados autofirmados).

`wintun.dll` ya viene firmado por WireGuard LLC y no se vuelve a firmar.

## Opción 1: Azure Artifact Signing (antes Trusted Signing). Firma automática en CI

Es la vía recomendada por Microsoft. Cuesta ~USD 10/mes (plan Basic). La disponibilidad para
personas naturales depende del país; compruébalo en el portal al crear la cuenta.

1. En el [portal de Azure](https://portal.azure.com), crea un recurso **Artifact Signing account**
   y anota su **endpoint** (p. ej. `https://eus.codesigning.azure.net/`).
2. Completa la **validación de identidad** (Individual u Organización) y crea un
   **Certificate profile** de tipo **Public Trust**.
3. Crea un **App registration** (Microsoft Entra ID) con un *client secret* y asígnale el rol
   **Artifact Signing Certificate Profile Signer** sobre la cuenta de firma.
4. En GitHub → *Settings → Secrets and variables → Actions* del repositorio:
   - **Secrets**: `AZURE_TENANT_ID`, `AZURE_CLIENT_ID`, `AZURE_CLIENT_SECRET`
   - **Variables**: `AZURE_SIGNING_ENDPOINT`, `AZURE_SIGNING_ACCOUNT`, `AZURE_CERT_PROFILE`
5. Publica una versión (`git tag vX.Y.Z && git push --tags`). El job `windows-sign` firma
   `MyVpn.exe`, `MyVpnTray.exe` y `myvpnd.exe`, verifica las firmas y la Release sale firmada.

Mientras esos secretos no existan, el CI publica los binarios sin firmar y lo avisa con un *warning*.

## Opción 2: certificado propio en token o en la nube (p. ej. Certum). Firma local

Los certificados de firma de código actuales no se entregan como archivo `.pfx` (la clave vive en
un token USB o en un HSM en la nube, como Certum SimplySign), así que no se pueden usar en CI y se
firma desde un PC con Windows:

1. Obtén un certificado de firma de código **RSA** (Certum ofrece uno para proyectos de código
   abierto a personas naturales) y conéctalo (token o SimplySign Desktop).
2. Instala el **Windows SDK** (componente *Signing Tools*).
3. Busca la huella del certificado: `Get-ChildItem Cert:\CurrentUser\My -CodeSigningCert`.
4. Descarga los `.zip` de Windows de la Release y ejecuta:
   ```powershell
   .\scripts\sign-windows.ps1 -Thumbprint <HUELLA> -Zip MyVpn-X.Y.Z-windows-amd64.zip,MyVpn-X.Y.Z-windows-arm64.zip
   gh release upload vX.Y.Z signed\*.zip --clobber
   ```

## Comprobación

En Windows: clic derecho en `MyVpn.exe` → *Propiedades* → pestaña **Firmas digitales**, o
`Get-AuthenticodeSignature .\MyVpn.exe` (debe decir `Valid`).
