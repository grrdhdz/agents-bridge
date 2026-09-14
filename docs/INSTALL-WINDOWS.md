# Instalación de `codex-bridge` en Windows

El servidor y el orquestador viven en la Mac. Windows solo ejecuta el cliente
con el comando de unión que imprime la Mac. Instala y conecta la aplicación
Tailscale en ambos equipos; Windows no necesita el CLI de Tailscale.

## Opción reproducible: Go

Requiere Go 1.27 o posterior:

```powershell
go install github.com/grrdhdz/codex-agents-bridge/cmd/codex-bridge@latest
codex-bridge.exe join --host <magicdns-del-mac> --port <puerto> --instance <instance_id> --token <token>
```

Si `codex-bridge.exe` no se reconoce, ejecuta `%GOBIN%\codex-bridge.exe` o
`%USERPROFILE%\go\bin\codex-bridge.exe`, según la configuración de Go.

## Opción binaria: GitHub Release

Descarga `codex-bridge-windows-amd64.exe` y `SHA256SUMS` desde:

<https://github.com/grrdhdz/codex-agents-bridge/releases/latest>

Verifica el archivo antes de usarlo:

```powershell
$expected = ((Select-String -Path .\SHA256SUMS -Pattern 'codex-bridge-windows-amd64.exe').Line -split '\s+')[0].ToLowerInvariant()
$actual = (Get-FileHash .\codex-bridge-windows-amd64.exe -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actual -ne $expected) { throw 'SHA256 no coincide' }
```

Después pega exactamente el comando mostrado por la Mac, sustituyendo el
nombre por `codex-bridge-windows-amd64.exe` si no renombraste el archivo:

```powershell
.\codex-bridge-windows-amd64.exe join --host <magicdns-del-mac> --port <puerto> --instance <instance_id> --token <token>
```

No copies IPs, tokens ni configuración de proyectos a archivos. El token de
emparejamiento se consume una sola vez y las credenciales, mensajes e historial
de la instancia solo viven en RAM.
