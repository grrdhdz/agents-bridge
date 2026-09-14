# Codex Agents Bridge

`codex-bridge` es una miniaplicación TUI de texto para mantener un canal redundante
entre un orquestador en macOS y un ejecutor en Windows. El mensaje oficial se
envía primero por Codex y después se copia y pega exactamente igual en la TUI.
La primera versión no automatiza ni inspecciona Codex.
La versión publicada actual es `v0.1.2`.

## Propiedades del MVP

- Cada ejecución de `codex-bridge` en Mac crea un proceso/instancia aislada con un
  `instance_id`, un puerto TCP efímero y un token de emparejamiento de un solo
  uso.
- El servidor vive en el proceso Mac. Windows se une como único ejecutor.
- Tailscale se usa únicamente para conectividad: la Mac obtiene su IPv4 con
  `tailscale ip --4` y su nombre MagicDNS desde `tailscale status --json`.
  `codex-bridge` busca primero `tailscale` en PATH y, en macOS, también reconoce el
  CLI oficial del bundle `/Applications/Tailscale.app/Contents/MacOS/Tailscale`
  (o el bundle equivalente dentro de `~/Applications`). En Windows conserva
  PATH y añade las ubicaciones estándar de `Program Files`; no modifica la
  instalación ni la configuración de Tailscale.
- No se usa daemon global, configuración por proyecto, SQLite, archivos de
  estado ni logs de cuerpos de mensajes. Todo vive en RAM y desaparece al
  cerrar o perder el proceso.
- El historial y la cola se conservan durante una caída de red mientras el
  proceso Mac siga vivo. Una caída del proceso Mac no tiene recuperación.
- El cuerpo canónico es texto UTF-8 intacto. No se admiten adjuntos.

## Compilar

Requiere Go 1.27 o posterior y Tailscale instalado en la Mac del
orquestador. El cliente Windows solo necesita la aplicación Tailscale activa;
no necesita el CLI de Tailscale.

```sh
go mod tidy
go test ./...
go build -o codex-bridge ./cmd/codex-bridge
GOOS=windows GOARCH=amd64 go build -o codex-bridge.exe ./cmd/codex-bridge
```

Comprueba la versión del binario con `codex-bridge --version`.

## Instalación Windows

La opción reproducible desde código fuente es instalar el paquete del módulo:

```powershell
go install github.com/grrdhdz/codex-agents-bridge/cmd/codex-bridge@latest
codex-bridge.exe join --host <magicdns-del-mac> --port <puerto> --instance <instance_id> --token <token>
```

`go install` coloca el ejecutable en `%GOBIN%` o, por defecto,
`%USERPROFILE%\go\bin`; agrega esa carpeta al PATH de tu sesión si Windows no
la encuentra automáticamente.

También hay un binario Windows amd64 y su checksum en la [última release](https://github.com/grrdhdz/codex-agents-bridge/releases/latest):

```powershell
Invoke-WebRequest https://github.com/grrdhdz/codex-agents-bridge/releases/latest/download/codex-bridge-windows-amd64.exe -OutFile .\codex-bridge.exe
Invoke-WebRequest https://github.com/grrdhdz/codex-agents-bridge/releases/latest/download/SHA256SUMS -OutFile .\SHA256SUMS
$expected = ((Select-String -Path .\SHA256SUMS -Pattern 'codex-bridge-windows-amd64.exe').Line -split '\s+')[0].ToLowerInvariant()
$actual = (Get-FileHash .\codex-bridge.exe -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actual -ne $expected) { throw 'SHA256 no coincide' }
.\codex-bridge.exe join --host <magicdns-del-mac> --port <puerto> --instance <instance_id> --token <token>
```

Consulta también [`docs/INSTALL-WINDOWS.md`](docs/INSTALL-WINDOWS.md). El
comando de unión completo lo imprime `codex-bridge` en la Mac; no configures
proyectos ni `project_id` en ningún repositorio.

## Flujo de emparejamiento

En la Mac del orquestador:

```sh
./codex-bridge
```

La aplicación detecta la IPv4 Tailscale, crea la instancia y muestra un
comando `codex-bridge join ...` completo. Pega ese comando al agente Windows o
transfiérelo por el canal que uses para coordinar los agentes.
En la TUI y en la salida inicial se muestra en varias líneas con continuaciones
de PowerShell para que el host, la instancia y el token completo no se recorten
en terminales estrechas; pega todas las líneas juntas en PowerShell.

En Windows:

```powershell
codex-bridge.exe join --host <magicdns-del-mac> --port <puerto> --instance <instance_id> --token <token>
```

El token se consume una sola vez. Después del emparejamiento el cliente usa un
token de reconexión que solo vive en memoria. Si Windows se cierra, hay que
generar un nuevo token desde Mac con `/pair`.

## Controles de la TUI

- `Ctrl+Enter` o `Ctrl+S`: enviar el texto pegado (ambos son atajos de envío).
- `Enter`: insertar una nueva línea; el pegado multilínea se conserva.
- `PgUp` / `PgDn`, `Home` / `End`: desplazarse por el historial RAM.
- `/status`: estado de conexión y tamaño de la cola temporal.
- `/pair`: solo en Mac, invalida el token anterior y muestra uno nuevo.
- `/stop`: solo en Mac, cierra la instancia y elimina todo su estado.
- `/quit` o `Ctrl+C`: cierra la instancia Mac o el cliente Windows.

Los mensajes propios aparecen a la derecha y los remotos a la izquierda. La
orientación se calcula con `sender_role`; no se almacenan mensajes distintos
por equipo.

## Protocolo y límites

El transporte es TCP con frames JSON delimitados por nueva línea. Cada mensaje
incluye `instance_id`, `message_id`, `client_seq`, `server_seq`, `sender_id`,
`sender_role`, `kind`, `body`, `body_sha256`, `source` y marcas de tiempo.

El servidor confirma el mensaje en RAM antes de enviar `accepted`. El receptor
responde `ack`; el servidor confirma ese ACK con `ack_confirmed` y el emisor ve
`delivered`. El cliente solo avanza su cursor de replay con confirmaciones
servidoras: ACK confirmado para mensajes remotos o aceptación local para sus
propios mensajes históricos. Una caída antes de confirmar un mensaje remoto
vuelve a entregarlo sin perderlo; los mensajes propios históricos se renderizan
sin generar ACK.
Reintentar el mismo `message_id` con el mismo cuerpo y repetir un ACK son
idempotentes; reutilizarlo con otro cuerpo devuelve `ID_CONFLICT`.

Cada conexión usa frames `ping`/`pong`, deadlines de lectura/escritura y un
intervalo de heartbeat para detectar conexiones half-open. Una pérdida de red
marca la TUI como reconectando; mientras el proceso Mac siga vivo, la
reconexión reanuda el cursor y el historial RAM de esa instancia.

Una nueva unión después de `/pair` recibe el historial completo que aún vive en
RAM, en orden de `server_seq`, para reconstruir la conversación sin mezclar
instancias.

Límites predeterminados:

- 256 KiB por cuerpo.
- ~1,75 MiB por frame JSON (incluye el peor caso de escape JSON de un cuerpo de
  256 KiB).
- 1.000 mensajes por instancia.
- 8 MiB de historial por instancia.
- 256 mensajes o 2 MiB de cola RAM por cliente.

Cuando se alcanza un límite, la aplicación devuelve un error explícito y no
descarta mensajes silenciosamente. `/pair` rechaza la rotación mientras el
ejecutor actual está conectado; al aceptar un nuevo emparejamiento se genera
siempre un token de reconexión nuevo y el anterior queda invalidado.

## Advertencias

El historial no es durable. Cerrar o perder el proceso Mac destruye instancia,
mensajes, colas, presencia y credenciales. Cambiar de red en Mac no destruye
la instancia si el proceso sigue vivo y Tailscale vuelve a anunciar el mismo
nombre; el cliente intenta reconectar y solicita los `server_seq` faltantes.
