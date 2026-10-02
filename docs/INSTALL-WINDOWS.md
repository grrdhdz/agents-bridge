# Instalación de `agents-bridge` en Windows

El servidor y el orquestador viven en la Mac. Windows solo ejecuta el cliente
con el comando de unión que imprime la Mac. Instala y conecta la aplicación
Tailscale en ambos equipos; Windows no necesita el CLI de Tailscale.
La Mac imprime el comando en varias líneas de PowerShell con backticks de
continuación; pega todas las líneas para conservar el token completo.

En ambas TUI, `PgUp`/`PgDn`, `Home`/`End`, la rueda y el gesto vertical del
trackpad desplazan el historial. La Mac también permite `F5` para volver a
copiar el comando de unión; Windows no muestra ese control.

Este nombre se publicará con v0.5. Mientras tanto, compila el checkout
según el [README](../README.md#compilar). Los ejemplos de instalación
siguientes corresponden a las publicaciones con el nombre nuevo.

## Opción reproducible: Go

Requiere Go 1.27 o posterior:

```powershell
go install github.com/grrdhdz/agents-bridge/cmd/agents-bridge@latest
agents-bridge.exe --version
agents-bridge.exe join --host <magicdns-del-mac> --port <puerto> --instance <instance_id> --token <token>
```

Si `agents-bridge.exe` no se reconoce, ejecuta `%GOBIN%\agents-bridge.exe` o
`%USERPROFILE%\go\bin\agents-bridge.exe`, según la configuración de Go.

## Opción binaria: GitHub Release

Descarga `agents-bridge-windows-amd64.exe` y `SHA256SUMS` desde:

<https://github.com/grrdhdz/agents-bridge/releases/latest>

Verifica el archivo antes de usarlo:

```powershell
$expected = ((Select-String -Path .\SHA256SUMS -Pattern 'agents-bridge-windows-amd64.exe').Line -split '\s+')[0].ToLowerInvariant()
$actual = (Get-FileHash .\agents-bridge-windows-amd64.exe -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actual -ne $expected) { throw 'SHA256 no coincide' }
```

Después pega exactamente el comando mostrado por la Mac, sustituyendo el
nombre por `agents-bridge-windows-amd64.exe` si no renombraste el archivo:

```powershell
.\agents-bridge-windows-amd64.exe join --host <magicdns-del-mac> --port <puerto> --instance <instance_id> --token <token>
```

No copies IPs, tokens ni configuración de proyectos a archivos. El token de
emparejamiento se consume una sola vez y las credenciales, mensajes e historial
de la instancia solo viven en RAM.
