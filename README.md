# Codex Agents Bridge

`codex-bridge` es una miniaplicación TUI de texto para mantener un canal redundante
entre un orquestador en macOS y un ejecutor en Windows. El mensaje oficial se
envía primero por Codex y después se copia y pega exactamente igual en la TUI.
La primera versión no automatiza ni inspecciona Codex.
La versión publicada actual es `v0.2.1`: corrige en Windows lo encontrado al
verificar `v0.2.0`, que añadió el modo local entre agentes del mismo equipo y
el control no gráfico `ctl`.

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

La aplicación detecta la IPv4 Tailscale, crea la instancia y copia
automáticamente el comando completo `codex-bridge join ...` al portapapeles de
la Mac. Pégalo en Codex o PowerShell para el agente Windows. La salida inicial
de la terminal conserva el comando completo como fallback seguro si la copia
falla; no se muestra el token en la vista normal de la TUI. Pulsa `F5` para
volver a copiarlo. `/pair` genera y copia del mismo modo un comando nuevo.

En Windows:

```powershell
codex-bridge.exe join --host <magicdns-del-mac> --port <puerto> --instance <instance_id> --token <token>
```

El token se consume una sola vez. Después del emparejamiento el cliente usa un
token de reconexión que solo vive en memoria. Si Windows se cierra, hay que
generar un nuevo token desde Mac con `/pair`.

## Tailscale sin TUI (`--headless`)

Cuando el orquestador o el ejecutor no son personas frente a una terminal sino
agentes (por ejemplo un agente remoto en Windows actuando como ejecutor), cada
lado puede arrancar sin TUI y hablar solo por `ctl`:

En la Mac (orquestador/host):

```sh
codex-bridge --headless --ready-file /ruta/ready.json
```

`--headless` en el host exige `--ready-file` (si falta, es un error de uso,
salida 2): el comando de unión completo —con su token de un solo uso— se
escribe **solo** en el campo `join_command` de ese archivo (`0600`, o ACL
exclusiva del usuario en Windows; creación exclusiva), nunca en stdout ni
stderr. El archivo también trae la línea
`ready` habitual (`{"type":"ready","instance_id":...,"mode":"tailscale-host",
"join_command":"..."}`). `--idle-timeout` sigue con valor por defecto `0`
(desactivado) en este modo: el puente humano Mac↔Windows no debe cerrarse solo
por inactividad.

En el otro equipo, un agente ejecutor se une igual sin TUI:

```sh
codex-bridge join --host <magicdns-del-mac> --port <puerto> --instance <instance_id> --token <token> --headless
```

(los cuatro flags salen de `join_command`, tal cual, o un humano se los pasa a
mano). Publica su propio descriptor `tailscale-join`, imprime la línea `ready`
en stdout (y en `--ready-file` si se indica) y solo reconecta en segundo
plano; un agente en ese equipo usa
`codex-bridge ctl --instance-id <id> --role executor ...` normalmente.

En cualquiera de los dos equipos, el usuario observa e interviene en la
conversación con `codex-bridge tui --instance-id <id>` (ver más abajo) sin
tocar el flujo de los agentes; y `codex-bridge stop --instance-id <id>` cierra
el proceso local de ese equipo (el host o el join), no el otro lado.

## Controles de la TUI

- `Ctrl+Enter` o `Ctrl+S`: enviar el texto pegado (ambos son atajos de envío).
- `Enter`: insertar una nueva línea; el pegado multilínea se conserva.
- `PgUp` / `PgDn`, `Home` / `End`, rueda del mouse o gesto vertical del
  trackpad: desplazarse por el historial RAM; los párrafos largos se ajustan al
  ancho disponible.
- `/status`: estado de conexión y tamaño de la cola temporal.
- `F5`: solo tiene efecto de copia en la TUI de Mac; vuelve a copiar el comando
  completo de unión sin mostrar sus credenciales.
- `/pair`: solo en Mac, invalida el token anterior, genera y copia uno nuevo.
- `/stop`: solo en Mac, cierra la instancia y elimina todo su estado.
- `/quit` o `Ctrl+C`: cierra la instancia Mac o el cliente Windows.

Los mensajes propios aparecen a la derecha y los remotos a la izquierda. La
orientación se calcula con `sender_role`; no se almacenan mensajes distintos
por equipo.

## Modo local entre agentes (v0.2.0)

Para dos agentes en el mismo equipo (por ejemplo Claude Code como orquestador y
un agente de la app Codex como ejecutor) no hace falta Tailscale, TUI ni
emparejamiento:

```sh
codex-bridge local
```

El proceso escucha solo en `127.0.0.1` y aloja ambos roles. Si su stdout es
una terminal real y no se pasa `--headless`, muestra directamente su propia
TUI observadora (igual que `codex-bridge tui`, ver abajo, pero sobre su
propio endpoint de orquestador) en vez de quedarse en silencio; aquí la TUI
**es** el proceso: cerrarla con `Ctrl+C`, `/quit` o un `/stop` confirmado
cierra el puente entero (a diferencia de una `codex-bridge tui` separada,
donde `/quit` solo cierra esa ventana y deja el puente vivo). Para
distinguirlas, esta TUI se titula `CODEX-BRIDGE LOCAL` y su pie dice
`/quit cierra el puente`; la separada se titula `CODEX-BRIDGE OBSERVADOR` y
dice `/quit cierra esta ventana`. En ese modo no
se imprime la línea `ready` en stdout —usa `--ready-file` para conocer el
`instance_id` desde otro lado—. Con `--headless`, o si stdout no es una
terminal (el caso normal cuando un agente lo lanza), imprime la línea
`{"type":"ready","instance_id":...}` sin secretos y queda en segundo plano.

Cualquiera de los dos modos se cierra con `Ctrl+C`, `SIGTERM`,
`codex-bridge stop` (ver abajo) o tras `--idle-timeout` (por defecto `30m`;
`0` lo desactiva) sin actividad, y borra todo su estado. Actividad es
cualquier mensaje publicado o recibido, o un `ctl wait`/`watch` en curso del
lado orquestador (la propia TUI, embebida o separada, cuenta así, porque usa
`watch`); el `wait` del ejecutor no cuenta, para que un puente sin
orquestador no quede vivo para siempre.

`--ready-file RUTA` escribe la línea `ready` en ese archivo (permisos
`0600`, o en Windows una ACL protegida solo para el usuario actual; creación
exclusiva: falla si ya existe) además de, o en lugar de,
stdout según el modo; útil para que quien lanzó `local` en la terminal del
usuario conozca el `instance_id` sin buscarlo con `ps`. El archivo aparece de
una vez, ya completo, cuando el puente está listo (se escribe en un temporal
privado y se enlaza en su sitio), así que basta con esperar a que exista.

Cada agente usa `ctl` con su `instance_id` (de la línea `ready`) y su rol; el
usuario puede tener varios puentes a la vez, así que `--instance-id` es
obligatorio en todo comando salvo `ctl list`:

```sh
printf 'TAREA\n...\n' | codex-bridge ctl send --instance-id <id> --role orchestrator --body-file -
codex-bridge ctl wait --instance-id <id> --role executor --timeout 5m --format text
codex-bridge ctl read --instance-id <id> --role orchestrator --after-event-seq 0
codex-bridge ctl watch --instance-id <id> --role executor
codex-bridge ctl list
```

En Windows PowerShell 5.1, lo que se canaliza a un programa llega con la
codificación de `$OutputEncoding` (por defecto ASCII: `ó` y `ñ` se convierten
en `?` **antes** de llegar a `codex-bridge`). Configura UTF-8 sin BOM en la
sesión antes de enviar:

```powershell
$utf8 = New-Object Text.UTF8Encoding $false
$OutputEncoding = $utf8; [Console]::InputEncoding = $utf8; [Console]::OutputEncoding = $utf8
"TAREA`nDescripción…" | codex-bridge ctl send --instance-id <id> --role orchestrator --body-file -
```

`ctl send` quita los BOM UTF-8 iniciales, convierte CRLF en LF y acepta un
`--body-file` en UTF-16 con BOM (lo que escribe `>` en PowerShell 5.1), así que
la primera línea llega exactamente como la etiqueta (`TAREA`, `RESULTADO`…).

`ctl wait` devuelve el siguiente mensaje del otro rol y lo confirma (el emisor
lo ve `delivered`) solo después de entregarlo; si el comando se corta antes, el
mensaje se vuelve a entregar. Los modos con TUI también publican su descriptor,
así que `ctl` funciona igual en Mac y Windows.

Cuando el ejecutor es un agente de la app de Codex, el orquestador puede abrir
su chat con el prompt ya escrito (el usuario pulsa Enter):

```sh
codex-bridge codex open --thread 'codex://threads/<id>' --instance-id <id> --prompt-file -
```

Para ver y cerrar los puentes del usuario, sin depender de `ctl`:

```sh
codex-bridge ps                       # tabla: instancia, modo, roles, pid, inicio, inactividad, peer, mensajes
codex-bridge ps --format jsonl
codex-bridge stop --instance-id <id>  # cierre limpio, equivalente a Ctrl+C en ese proceso
```

`ps` agrupa por `instance_id` (un puente `local` aporta una sola fila con
ambos roles) y nunca muestra `control_url`, `capability` ni `cwd`. `MSGS` es
el `latest_server_seq` del endpoint consultado (el del orquestador, si vive en
este equipo): cuántos mensajes del otro rol ha confirmado ya ese lado, no el
total de la conversación. `stop`
elige automáticamente el endpoint que puede cerrar ese puente (el
orquestador en `local`; el rol del ejecutor no puede cerrarlo y responde
`FORBIDDEN`); si la instancia no existe, sale con código 3, y si su puerto no
responde, con código 8 e imprime el PID por si hace falta un `kill` manual.

Para ver la conversación en vivo e intervenir sin ser un agente:

```sh
codex-bridge tui --instance-id <id>
```

Se conecta por el plano de control (nunca por el protocolo TCP), así que
funciona igual para `local`, el host Mac y `join`: usa el descriptor del
orquestador si vive en este equipo, o si no el del ejecutor. Muestra el
historial completo y los mensajes en vivo de ambos roles (`Orquestador` a la
derecha, `Ejecutor` a la izquierda), con hora y origen
(`agente` para `agent-control`, `humano` para `human-operator` o
`manual-codex-copy`); lo que el usuario escribe (Ctrl+Enter/Ctrl+S) se envía
con `source=human-operator`, visible también en la cabecera de texto de
`ctl wait` para que el agente sepa que ese mensaje viene del usuario y tiene
prioridad. Esta TUI **nunca confirma mensajes**: usa `watch`, así que el
`ctl wait` del agente los sigue recibiendo igual, la haya visto o no.
`/stop` pide una segunda confirmación (escribirlo de nuevo) antes de llamar a
`stop`; `/quit` o `Ctrl+C` cierran solo la TUI, el puente sigue vivo. Usa la
pantalla alternativa de la terminal, así que al salir la deja como estaba.

Los descriptores de control viven en un directorio privado del usuario
(`0700`; en Windows, `%LOCALAPPDATA%\Temp\codex-bridge\<usuario>\instances`
con una ACL protegida solo para el usuario actual). Un directorio de Windows
creado por v0.2.0, con la ACL heredada de `%TEMP%`, se endurece solo la
primera vez; si pertenece a otro usuario, `ctl` se desactiva con un error que
pide borrarlo.

El flujo completo para agentes está en la skill
[`.agents/skills/codex-bridge`](.agents/skills/codex-bridge/SKILL.md) y el
diseño en
[`docs/superpowers/specs/2026-09-27-local-agents-design.md`](docs/superpowers/specs/2026-09-27-local-agents-design.md).

## Protocolo y límites

El transporte es TCP con frames JSON delimitados por nueva línea. Cada mensaje
incluye `instance_id`, `message_id`, `client_seq`, `server_seq`, `sender_id`,
`sender_role`, `kind`, `body`, `body_sha256`, `source` y marcas de tiempo.
Los roles viajan con sus identificadores históricos del protocolo v1,
`mac-orchestrator` y `win-executor` (también en `sender_id`, en `ctl read` y
en los nombres de los descriptores), incluso en modo `local` y en cualquier
sistema; cambiarlos rompería la compatibilidad del protocolo. La CLI acepta y
muestra `orchestrator` y `executor`.

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
