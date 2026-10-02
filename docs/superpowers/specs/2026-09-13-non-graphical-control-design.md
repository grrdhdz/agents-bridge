# Especificación de control no gráfico de Codex Bridge

> Documento histórico anterior al renombre de 2026-10-01. Las referencias a
> `codex-bridge` describen esa versión; para el uso actual consulta la sección
> «Migración desde codex-bridge» del [README](../../../README.md).

Estado: aprobado para diseño de v0.2.0

Fecha: 2026-09-13

Commit base: `6158257eda6a8409a36dbd9799309661a13d0c4d` (`v0.1.3`)

## 1. Propósito

Codex Bridge v0.2.0 añadirá un acceso local, no gráfico y orientado a agentes
para leer y enviar mensajes de una instancia viva de `codex-bridge`. La TUI
humana seguirá funcionando simultáneamente y conservará exactamente la
semántica del protocolo actual.

El acceso tendrá cuatro operaciones:

- `list`: descubrir las instancias locales vivas del usuario.
- `read`: leer eventos RAM desde un cursor de `event_seq`.
- `watch`: observar eventos nuevos y replay desde un cursor de `event_seq`.
- `send`: publicar texto con idempotencia explícita.

La interfaz será HTTP sobre loopback por proceso, con registros JSONL en las
respuestas normales y en el stream de `watch`. El transporte TCP entre Mac y
Windows no cambia.

## 2. No objetivos

Esta versión no hará lo siguiente:

- No añadirá MCP ni un sidecar MCP.
- No automatizará la lectura o escritura del canal oficial de Codex.
- No persistirá cuerpos, historial, colas, presencia, pairing tokens ni
  reconnect tokens.
- No creará un daemon global de chat ni un coordinador remoto.
- No permitirá que `ctl` ejecute `/stop` o `/pair`; esas acciones siguen bajo
  la autoridad de la TUI del orquestador Mac.
- No expondrá el endpoint HTTP por Tailscale, LAN, MagicDNS o una interfaz
  distinta de loopback.
- No introducirá `project_id`, configuración por repositorio ni selección
  basada en rutas compartidas entre Mac y Windows.
- No cambiará la versión ni los frames del protocolo TCP v1 de v0.1.x.

## 3. Decisiones invariantes

1. Cada invocación de `codex-bridge` crea una instancia nueva, un
   `instance_id` nuevo y un endpoint HTTP nuevo.
2. Instancias concurrentes nunca comparten historial, colas, capacidades,
   cursores ni presencia.
3. El `instance_id` es la identidad de aislamiento. No se deriva de la ruta
   local ni del nombre del proyecto.
4. Todo mensaje y journal permanecen en RAM y desaparecen cuando termina el
   proceso que posee la instancia.
5. Mac sigue alojando el `Server` y Windows sigue siendo el cliente remoto.
6. Tailscale se usa únicamente para tráfico Mac↔Windows del protocolo TCP.
7. La TUI y el control no gráfico consumen una misma fuente de eventos
   multiplexada; nunca compiten por leer `Client.Events()`.
8. El endpoint de control actúa con el rol del `Client` al que pertenece. La
   petición no puede elegir ni falsificar `sender_role`.
9. El `EventHub` asigna un `event_seq` local, monotónico y único por proceso e
   instancia a cada evento emitido: mensaje, delivery, estado, transporte y
   lifecycle. `server_seq` queda reservado para ordenar mensajes canónicos y
   referenciarlos en ACK/delivery.

## 4. Arquitectura

Cada proceso tendrá dos superficies independientes:

    TUI humana ───────────────┐
                              ├─ EventHub ─ Client ─ TCP v1 ─ Server Mac
    HTTP loopback / ctl ──────┘                         └─ cliente Windows

El `Server` Mac y cada `Client` Windows/Mac conservan su estado actual en RAM.
El nuevo `ControlHTTP` vive dentro del mismo proceso y se enlaza a
`127.0.0.1:0`. El sistema operativo asigna un puerto distinto a cada
instancia; nunca se reutiliza un puerto fijo ni se crea un listener global.

El endpoint HTTP se inicia después de que la instancia y su `Client` local
estén autenticados. Solo entonces se publica su descriptor. Si no se puede
crear el endpoint o el descriptor, la TUI puede continuar mostrando el
puente, pero informa que `ctl` no está disponible; esa falla no convierte el
protocolo Mac↔Windows en un estado parcialmente autenticado.

Endpoints de una instancia:

    GET  /v1/health
    GET  /v1/read?after_event_seq=N&limit=L
    GET  /v1/watch?after_event_seq=N
    POST /v1/send

Todos requieren `Authorization: Bearer CAPABILITY`. No habrá rutas HTTP para
rotar pairing, cerrar el servidor, alterar Tailscale o inspeccionar secretos.

## 5. Descriptor efímero y descubrimiento

El descriptor es únicamente metadata de descubrimiento y capacidad de acceso.
No contiene cuerpos, envelopes, historial, colas, tokens de pairing ni tokens
de reconexión.

### 5.1 Ubicación por plataforma

En macOS la ubicación lógica es:

    $TMPDIR/codex-bridge/$UID/instances/$INSTANCE_ID.json

En Windows la ubicación lógica es:

    %LOCALAPPDATA%\Temp\codex-bridge\CURRENT_USER_SID\instances\INSTANCE_ID.json

`$UID`, `CURRENT_USER_SID` e `INSTANCE_ID` se resuelven en tiempo de ejecución
con la identidad del usuario y la instancia recién creada; no son nombres
literales ni configuración que el usuario deba proporcionar.

La implementación debe crear el directorio raíz con permisos de propietario
solamente: `0700` en macOS y ACL exclusiva del usuario actual en Windows. El
archivo debe ser de propietario solamente (`0600` en macOS y la ACL equivalente
en Windows). Si no se pueden comprobar esos permisos, no se debe publicar una
capability en un archivo y `ctl` queda deshabilitado con error accionable.

La escritura es atómica: archivo temporal del mismo directorio, `fsync` del
contenido y rename al nombre final. El descriptor se considera válido solo si
su JSON completo y su ownership son correctos.

### 5.2 Campos exactos

Cada descriptor contiene exactamente estos campos de control, más ningún dato
de chat:

    {
      "descriptor_version": 1,
      "instance_id": "INSTANCE_ID",
      "pid": 12345,
      "local_role": "mac-orchestrator",
      "control_url": "http://127.0.0.1:54321",
      "capability": "BASE64URL_32_RANDOM_BYTES",
      "cwd": "/ruta/local/de/arranque",
      "started_at": "2026-09-13T12:00:00Z",
      "heartbeat_at": "2026-09-13T12:00:03Z",
      "expires_at": "2026-09-13T12:00:18Z"
    }

`capability` se genera con el CSPRNG del sistema y permanece en RAM del proceso
y en el descriptor protegido mientras la instancia está viva; por tanto, el
descriptor es un secreto efímero protegido, no persistencia de historial. La
capability nunca se imprime. `cwd` sirve únicamente para la auto-selección
local; `list` no lo devuelve. El endpoint HTTP verifica que la capability
coincida con comparación en tiempo constante.

El descriptor se actualiza cada 3 segundos. `expires_at` queda 15 segundos por
delante de `heartbeat_at`. La expiración es solo una señal de stale; no
autoriza a borrar una instancia que siga viva.

### 5.3 Limpieza normal y crash

En cierre normal, el proceso marca la instancia como cerrada, detiene el
endpoint HTTP, cierra el cliente/servidor y elimina el descriptor. La limpieza
no intenta conservar una copia del historial.

Después de un crash, el siguiente `ctl list` o arranque de `codex-bridge` hace
lo siguiente para cada descriptor:

1. Comprueba que el PID pertenece al usuario actual y sigue vivo.
2. Hace `GET /v1/health` con un timeout de 500 ms.
3. Solo si el proceso no existe o `expires_at` quedó atrás y el health check
   no responde, elimina el descriptor stale.
4. Si el proceso está vivo pero ocupado, conserva el descriptor y reporta el
   estado sin borrar datos.

El descriptor no ofrece recuperación después de la muerte del proceso. Tras
la eliminación, `read`, `watch` y `send` devuelven `INSTANCE_NOT_FOUND`.

### 5.4 Selección de instancia

`--instance-id INSTANCE_ID` siempre gana y es la selección inequívoca.

Si se omite `--instance-id`, `ctl read`, `ctl watch` y `ctl send` comparan el
`cwd` actual con el `cwd` de los descriptores del usuario:

- Cero coincidencias: `INSTANCE_NOT_FOUND`, salida 3.
- Una coincidencia: se selecciona automáticamente.
- Más de una coincidencia: `INSTANCE_AMBIGUOUS`, salida 2; se exige
  `--instance-id`.

Esto no crea configuración de proyecto: el `cwd` es metadata efímera de esa
ejecución y puede diferir entre Mac y Windows. `list` ignora el `cwd` y
devuelve todas las instancias del usuario actual sin exponer rutas ni
capabilities.

## 6. Comandos exactos

Los comandos escriben un registro JSON por línea. No existe una salida humana
paralela que pueda mezclar mensajes con JSONL.

    codex-bridge ctl list
    codex-bridge ctl read --instance-id INSTANCE_ID --after-event-seq N --limit L
    codex-bridge ctl watch --instance-id INSTANCE_ID --after-event-seq N
    codex-bridge ctl send --instance-id INSTANCE_ID --message-id MESSAGE_ID --body-file FILE

Los identificadores en mayúsculas de esta sintaxis son valores concretos de la
instancia o de la petición en tiempo de ejecución; no son decisiones pendientes
ni campos opcionales sin contrato.

Detalles de `ctl`:

- `list` no necesita `--instance-id`, `--after-event-seq` ni body.
- `--instance-id` es opcional en `read`, `watch` y `send` bajo la regla de
  auto-selección por `cwd`.
- `--after-event-seq` es opcional y por defecto es `0`.
- `--limit` es opcional y por defecto es `100`; el máximo es `1000`.
- `--message-id` es obligatorio para `send`; el agente debe conservarlo para
  reintentar de forma idempotente.
- `--body-file -` lee UTF-8 de stdin y conserva saltos de línea. No se acepta
  cuerpo por una opción de shell que pueda quedar expuesta en historial.
- El programa nunca imprime capability, pairing token, reconnect token o
  `control_url`.

Códigos de salida:

| Código | Significado |
|---:|---|
| 0 | Operación completada o stream cancelado limpiamente |
| 2 | Uso inválido, JSON inválido o instancia ambigua |
| 3 | Instancia inexistente, cerrada o descriptor stale |
| 4 | Capability ausente o inválida |
| 5 | Operación prohibida por rol o lifecycle |
| 6 | `message_id` en conflicto |
| 7 | Límite RAM, watcher lento o backpressure |
| 8 | Transporte local/remoto no disponible; reintento posible |
| 9 | Error interno no recuperable |

## 7. Protocolo JSONL v1

El protocolo de control usa `v: 1` y es independiente de
`protocol.Version == 1` del TCP. Cada registro termina en `\n`, y cada respuesta
de `watch` se envía con flush inmediato.

### 7.1 Transporte HTTP exacto

`ctl` genera un `request_id` nuevo por petición y lo transmite exclusivamente
en el header:

    X-Codex-Bridge-Request-ID: REQUEST_ID

Ese header es obligatorio en todos los `GET` y `POST`, tiene entre 1 y 128
caracteres ASCII y no contiene secretos. Para una petición válida, el servidor
copia el valor sin modificar en la respuesta JSONL. Si falta o no cumple el
formato, responde `400` y no inventa un `request_id`. No se acepta
`request_id` en la query string.

Todas las respuestas HTTP (`health`, `read`, errores y cada línea de `watch`;
`list` se resuelve leyendo descriptores) usan:

    Content-Type: application/x-ndjson; charset=utf-8

El `POST /v1/send` recibe exactamente un objeto JSON terminado por newline con
el mismo content type. El cliente no envía cuerpos adicionales. Antes de la
primera línea de `watch`, el servidor envía headers y obtiene
`http.Flusher`; después de escribir cada línea llama `Flush()`. Si ocurre un
error durante un stream ya iniciado, escribe un registro `error`, hace flush y
cierra la conexión; no intenta cambiar el status HTTP después de comenzar el
body.

Mapeo HTTP estable:

| HTTP | Situación |
|---:|---|
| 200 | `health`, `read` o respuesta normal |
| 202 | `send` encolado en RAM |
| 400 | JSON, query o header inválido |
| 401 | capability ausente o inválida |
| 403 | operación prohibida por rol/lifecycle |
| 404 | instancia o recurso inexistente |
| 409 | instancia ambigua o `message_id` en conflicto |
| 410 | instancia cerrada o descriptor stale confirmado |
| 413 | body o respuesta por encima del límite |
| 429 | backpressure o límite de watchers |
| 503 | transporte local/remoto no disponible |
| 500 | error interno |

Los códigos HTTP se traducen a los códigos de salida de `ctl` definidos en la
sección anterior. La salida 0 solo corresponde a status 200/202 y a
cancelación limpia de `watch`; una línea JSON de error siempre produce salida
distinta de cero.

### 7.2 Respuesta exitosa

    {
      "v": 1,
      "type": "response",
      "request_id": "REQUEST_ID",
      "ok": true,
      "operation": "read",
      "instance_id": "INSTANCE_ID"
    }

`request_id` es generado por el cliente y se devuelve sin cambios. No se usa
para deduplicar mensajes; la deduplicación de `send` usa `message_id`.

### 7.3 Error

    {
      "v": 1,
      "type": "error",
      "request_id": "REQUEST_ID",
      "ok": false,
      "code": "CURSOR_EXPIRED",
      "message": "requested cursor is older than the retained RAM journal",
      "retryable": false,
      "instance_id": "INSTANCE_ID",
      "oldest_event_seq": 42
    }

Los códigos son exactos:

    INVALID_JSON
    UNAUTHORIZED
    FORBIDDEN
    INSTANCE_NOT_FOUND
    INSTANCE_AMBIGUOUS
    INSTANCE_CLOSED
    CURSOR_EXPIRED
    MESSAGE_INVALID
    ID_CONFLICT
    CONTROL_BACKPRESSURE
    TRANSPORT_ERROR
    INTERNAL

Los errores nunca incluyen el body, capability, pairing token, reconnect token,
ruta local completa ni datos de conexión Tailscale.

### 7.4 `list`

La respuesta contiene todos los registros vivos del usuario actual:

    {
      "v": 1,
      "type": "response",
      "request_id": "REQUEST_ID",
      "ok": true,
      "operation": "list",
      "instances": [
        {
          "instance_id": "INSTANCE_ID",
          "local_role": "mac-orchestrator",
          "state": "running",
          "pid": 12345,
          "started_at": "2026-09-13T12:00:00Z",
          "heartbeat_at": "2026-09-13T12:00:03Z",
          "event_seq": 28,
          "latest_server_seq": 12,
          "oldest_event_seq": 1,
          "peer_connected": true
        }
      ]
    }

No se devuelven `cwd`, `control_url` ni `capability`.

### 7.5 `read`

`after_event_seq` es exclusivo: una petición con `after_event_seq: 28` solo
devuelve eventos con `event_seq >= 29`. `server_seq` dentro de un mensaje sigue
siendo el orden canónico del mensaje y no es cursor del control. El registro de
respuesta es:

`events` incluye todos los tipos retenidos por el `EventHub` (`message`,
`delivery`, `state`, `transport` y `lifecycle`), no solo cuerpos de mensajes.
Cada registro lleva su propio `event_seq`; solo los eventos que representan un
mensaje o su delivery pueden llevar también `server_seq`.

    {
      "v": 1,
      "type": "response",
      "request_id": "REQUEST_ID",
      "ok": true,
      "operation": "read",
      "instance_id": "INSTANCE_ID",
      "after_event_seq": 28,
      "events": [
        {
          "event": "message",
          "event_seq": 29,
          "server_seq": 13,
          "status": "delivered",
          "message": {
            "protocol_version": 1,
            "instance_id": "INSTANCE_ID",
            "message_id": "MESSAGE_ID",
            "client_seq": 4,
            "server_seq": 13,
            "sender_id": "mac-orchestrator",
            "sender_role": "mac-orchestrator",
            "kind": "chat",
            "body": "texto UTF-8 intacto",
            "body_sha256": "SHA256",
            "source": "manual-codex-copy",
            "created_at": "2026-09-13T12:01:00Z",
            "accepted_at": "2026-09-13T12:01:01Z"
          }
        }
      ],
      "next_after_event_seq": 29,
      "has_more": false
    }

El `message` es el envelope canónico existente; el control no crea una
representación distinta para la TUI.

### 7.6 `watch`

`watch` primero reenvía el replay posterior a `after_event_seq` y luego mantiene
la conexión HTTP abierta. Cada línea es un registro de evento:

    {"v":1,"type":"event","event":"message","instance_id":"INSTANCE_ID","event_seq":29,"server_seq":13,"status":"accepted","message":{}}
    {"v":1,"type":"event","event":"delivery","instance_id":"INSTANCE_ID","event_seq":30,"server_seq":13,"message_id":"MESSAGE_ID","status":"delivered"}

Los eventos de lifecycle y transporte tienen esta forma:

    {"v":1,"type":"event","event":"state","instance_id":"INSTANCE_ID","event_seq":31,"state":"reconnecting","latest_server_seq":13}

El consumidor confirma localmente su cursor procesado y, si se reconecta,
repite `watch` con ese `after_event_seq`. El servidor no avanza el cursor por el
simple hecho de haber escrito la línea; la lectura es at-least-once y la
deduplicación primaria se hace por `(instance_id, event_seq)`. `message_id` se
usa como deduplicación secundaria del mismo mensaje canónico.

Cerrar la conexión HTTP cancela el watcher mediante el contexto de la petición.

### 7.7 `send`

La petición tiene este JSON interno:

    {
      "v": 1,
      "message_id": "MESSAGE_ID",
      "body": "texto UTF-8 intacto"
    }

El `request_id` de este `POST` es únicamente el header
`X-Codex-Bridge-Request-ID`; no se duplica dentro del objeto JSON.

El servidor local no acepta `instance_id`, `sender_id` ni `sender_role` del
body de la petición; los obtiene del descriptor y del `Client` autenticado.

La respuesta inicial confirma la cola RAM local:

    {
      "v": 1,
      "type": "response",
      "request_id": "REQUEST_ID",
      "ok": true,
      "operation": "send",
      "status": "queued",
      "instance_id": "INSTANCE_ID",
      "message_id": "MESSAGE_ID",
      "client_seq": 8,
      "server_seq": 0,
      "queued_event_seq": 32
    }

Los estados posteriores llegan por `watch`:

- `queued`: aceptado en la cola RAM del `Client` local.
- `accepted`: aceptado por el `Server` y asignado `server_seq`.
- `delivered`: el interlocutor confirmó ACK.
- `rejected`: el `Server` rechazó el envelope o sus límites.
- `transport_error`: se perdió la conexión; el `Client` conserva la cola y
  reintenta mientras la instancia siga viva.

## 8. Cursor, límites y backpressure

- `after_event_seq` siempre es exclusivo y monotónico por proceso/instancia.
- `event_seq` se asigna a todo evento del EventHub, incluidos estados y
  deliveries; no se reutiliza ni se sustituye por `server_seq`.
- `server_seq` no se reinicia mientras viva la instancia y solo ordena mensajes
  canónicos dentro de envelopes y referencias de ACK/delivery.
- Journal de mensajes por `Client`: máximo 1.000 mensajes y 8 MiB de cuerpos,
  alineado con los límites del `Server`.
- Journal de eventos por `EventHub`: máximo 4.096 eventos y 4 MiB de metadata
  serializada. Un evento de mensaje referencia el envelope del journal de
  mensajes y no duplica su body para este límite. La referencia no elude el
  límite: el body existe una sola vez, en el journal de mensajes.
- La expulsión de un envelope por el límite de 1.000 mensajes o 8 MiB es
  coordinada. En la misma operación se expulsan todos los eventos que lo
  referencien, incluidos sus deliveries y estados asociados por
  `(message_id, server_seq)`. Ningún registro puede quedar apuntando a un body
  desalojado ni puede entregarse incompleto.
- Antes de retirar el body, la operación inspecciona las colas y cursores de
  los watchers. Un watcher que todavía pueda emitir una referencia afectada se
  cancela primero con `CURSOR_EXPIRED`; si ya tenía la cola llena, se cancela
  con `CONTROL_BACKPRESSURE`. Se descartan sus referencias no emitidas antes de
  desalojar el envelope, de modo que el body nunca pueda causar una entrega
  parcial.
- El límite de 4 MiB cuenta la metadata serializada de cada registro de evento
  una sola vez, incluida su referencia, pero nunca el body. Si ese límite
  expulsa eventos, se expulsan registros completos y se actualiza
  `oldest_event_seq`; no se conserva una referencia huérfana ni se expulsa por
  ello un envelope cuyo body aún cabe en su journal. El contrato at-least-once
  rige para eventos retenidos; una expulsión se comunica explícitamente como
  `CURSOR_EXPIRED` y nunca se presenta como replay completo.
- Después de cada expulsión, `oldest_event_seq` avanza al primer evento completo
  que aún permanece. Los eventos expulsados dejan huecos permanentes en la
  secuencia; `event_seq` no se reutiliza. Una lectura cuyo cursor caiga en esos
  huecos recibe `CURSOR_EXPIRED` con el nuevo `oldest_event_seq`.
- Un `watch` cuyo próximo evento requerido fue expulsado termina con
  `CURSOR_EXPIRED` y el nuevo `oldest_event_seq`. Si su cola acotada se llena
  antes de esa expulsión, termina con `CONTROL_BACKPRESSURE`; en ambos casos el
  consumidor debe reconectar con el último cursor confirmado y tratar el error
  explícito, sin salto silencioso.
- Repetir el mismo estado para `(message_id, server_seq)` no crea otro evento;
  solo las transiciones efectivas (`queued`, `accepted`, `delivered`,
  `rejected`, `transport_error`) consumen una posición de `event_seq`.
- `read --limit`: por defecto 100, máximo 1.000 registros y 2 MiB de JSON de
  respuesta.
- Cuerpo máximo: 256 KiB UTF-8, sin truncamiento silencioso.
- Máximo 8 watchers simultáneos por proceso.
- Cada watcher tiene una cola de 256 eventos o 2 MiB, lo que ocurra primero.
- Si la cola se llena, se emite `CONTROL_BACKPRESSURE`, se cierra el watcher y
  el consumidor debe reconectar desde su último `event_seq` confirmado.
- No se descartan eventos silenciosamente.
- Si el cursor solicitado ya no existe en el journal de eventos, se devuelve
  `CURSOR_EXPIRED` con `oldest_event_seq`; nunca se salta al evento más reciente
  sin notificarlo.

## 9. EventHub, TUI y concurrencia

El `Client` actual tiene un único canal consumido por `waitForFrame`. v0.2.0
lo sustituirá por un `EventHub` con:

- un solo lector de TCP por conexión;
- un journal RAM acotado;
- `Subscribe()` para la TUI;
- `Subscribe()` independiente para cada `watch`;
- fan-out con colas acotadas y cancelación por contexto;
- asignación de `event_seq` a cada evento emitido;
- deduplicación de eventos por `(instance_id, event_seq)` y de mensajes por
  `message_id`/`server_seq`;
- un mutex de escritura compartido por TUI, `send`, ACK y heartbeat.

La TUI seguirá recibiendo los mismos frames lógicos y conservará foco,
scroll, alineación, estados y controles humanos. Una petición `send` desde
`ctl` aparecerá en la TUI como si se hubiera enviado desde su input. Una
publicación de la TUI aparecerá en `watch` sin duplicarse.

El journal de Mac debe registrar también sus propios mensajes cuando `Publish`
los encola, porque el `Server` entrega al owner la confirmación `accepted`, no
un `FrameMessage` propio. El journal de Windows se rellena con el replay inicial
y los eventos posteriores. Ambos journals se destruyen al cerrar el proceso.

## 10. Autenticación y autorización local

1. La capability es independiente de owner token, pairing token y reconnect
   token.
2. Solo el descriptor protegido del usuario actual contiene la capability.
3. El HTTP server acepta exclusivamente conexiones desde `127.0.0.1` y exige
   `Authorization: Bearer`.
4. El cliente `ctl` verifica que `control_url` sea loopback y no sigue
   redirects.
5. La instancia decide el rol desde el `Client` asociado; el request no puede
   seleccionar un rol diferente.
6. `send` no puede publicar en otra instancia: la URL, capability y
   `instance_id` deben coincidir.
7. La capability nunca se imprime, nunca va en una opción de shell y nunca se
   registra en logs.
8. Un proceso malicioso del mismo usuario puede leer archivos de ese usuario;
   los permisos reducen el riesgo accidental, pero no prometen protección
   contra ese atacante local.

En plataformas donde se pueda usar identidad del sistema operativo para el
loopback, se preferirá sobre el bearer secreto. La capability seguirá siendo
necesaria como defensa adicional contra confusión de endpoint.

## 11. Aislamiento multi-instancia

Cada descriptor, endpoint, capability, journal, watcher y secuencia de
`event_seq` pertenece exactamente a un `instance_id`. `list` puede mostrar
varias instancias; todas las demás operaciones deben seleccionar una.

El control local no busca ni infiere rutas de otros checkouts. La única
selección implícita es igualdad de `cwd` en el mismo host, y solo cuando hay una
coincidencia. Mac y Windows pueden tener rutas distintas sin compartir ningún
identificador derivado de ellas.

No se expone descubrimiento remoto: para controlar una instancia se ejecuta
`ctl` en el host donde vive su proceso. Tailscale permanece estrictamente
fuera del plano de control.

## 12. Lifecycle

### Inicio

1. `bridge` genera el `instance_id` y sus credenciales RAM como hoy.
2. Se inicia el servidor TCP y el `Client` local.
3. Se inicia `EventHub` y `ControlHTTP` en loopback.
4. Se escribe el descriptor efímero.
5. Se inicia la TUI y el heartbeat del descriptor.

El comando solo anuncia una instancia después del paso 4. Si el paso 3 o 4
falla, no se imprime un descriptor incompleto.

### Cierre limpio

El cierre Mac continúa siendo exclusivo del orquestador. Antes de cerrar el
socket HTTP se marca la instancia como `closed`, se cancela cada watcher, se
elimina el descriptor y finalmente se destruyen servidor, cliente, journal y
capabilities de RAM. Windows recibe el `FrameClose` existente.

### Cierre inesperado

No hay recuperación de historial. El descriptor stale solo sirve para que el
siguiente descubrimiento lo retire; no permite reabrir el proceso ni obtener
mensajes antiguos.

## 13. Compatibilidad con v0.1.x

- El protocolo TCP v1 y sus frames no cambian.
- Un binario v0.1.x puede seguir conectándose por Tailscale a una instancia
  existente, pero no publica descriptor ni ofrece `ctl`.
- `ctl` v0.2.0 debe detectar un descriptor ausente y devolver
  `INSTANCE_NOT_FOUND`, no intentar interpretar un proceso v0.1.x.
- No se cambiarán pairing tokens, reconnect tokens, límites RAM ni la semántica
  de cierre de v0.1.x.
- La versión objetivo de la nueva interfaz es `v0.2.0`.

## 14. Componentes y archivos previstos

La implementación futura debe aislar el cambio en estos componentes:

    internal/control/descriptor.go          descriptor y stale cleanup
    internal/control/http.go                endpoints HTTP y JSONL
    internal/control/select.go              --instance-id y selección por cwd
    internal/control/types.go               esquema v1 y errores
    internal/control/descriptor_unix.go     runtime path y permisos macOS
    internal/control/descriptor_windows.go  runtime path y ACL Windows
    internal/bridge/eventhub.go             fan-out, journal y cancelación
    internal/bridge/client.go               integración del EventHub
    internal/tui/model.go                   suscripción en lugar de Events único
    cmd/codex-bridge/main.go                subcomando ctl

No se debe crear SQLite, un archivo de historial, un daemon global de chat ni
un paquete MCP en esta versión.

## 15. Plan TDD y verificación

Antes de implementar cada componente se deben añadir pruebas que fallen por la
razón correcta:

1. Descriptor: ruta por plataforma, permisos, atomicidad, capability protegida,
   ausencia de cuerpos y limpieza normal.
2. Stale cleanup: PID muerto, endpoint caído, TTL vencido, proceso vivo lento y
   no borrado accidental.
3. Descubrimiento: cero, una y varias instancias; selección por `cwd`; selección
   explícita por `instance_id`.
4. HTTP: JSONL, `X-Codex-Bridge-Request-ID`, content type, flush de `watch`,
   límites, códigos HTTP, códigos CLI y errores sin secretos.
5. Autenticación: capability correcta, incorrecta, endpoint no-loopback y
   petición de rol falsificado.
6. `read`: cursor exclusivo `after_event_seq`, límite, `CURSOR_EXPIRED`,
   `oldest_event_seq` y orden canónico interno por `server_seq`.
7. `watch`: `event_seq` único incluso cuando se repite `server_seq`, replay
   inicial, eventos nuevos, cancelación, reconexión y backpressure sin descarte
   silencioso.
8. Límite coordinado: al desalojar un envelope de 1.000/8 MiB también se
   desalojan sus referencias de eventos y estados, no se entrega ningún evento
   incompleto, avanza `oldest_event_seq`, y un cursor antiguo recibe
   `CURSOR_EXPIRED`; un watcher con cola llena recibe `CONTROL_BACKPRESSURE`.
9. `send`: UTF-8, multilínea, tamaño máximo, queued/accepted/delivered,
   reintento idempotente y `ID_CONFLICT`.
10. EventHub: TUI y dos watchers reciben el mismo evento; ningún consumidor
   roba eventos a otro; estados repetidos se coalescen; sin goroutine leaks bajo
   cancelación.
11. Aislamiento: dos instancias concurrentes no comparten mensajes, cursores de
    `event_seq`, capabilities ni descriptors.
12. Lifecycle: cierre Mac invalida control, elimina descriptor y no deja
    historial; crash queda stale y luego se limpia.
13. Verificación final: `go test ./...`, `go test -race ./...`, `go vet ./...`,
    build macOS arm64 y Windows amd64.

## 16. Amenazas y manejo de secretos

- **Lectura accidental del descriptor:** permisos `0700/0600` o ACL exclusiva;
  el descriptor no contiene mensajes.
- **Capability en logs o shell history:** nunca se incluye en argumentos,
  stdout, errores, URLs visibles ni logs.
- **Spoof de rol:** el rol se toma del `Client` local autenticado y no del
  JSON recibido.
- **Cruce de instancias:** todos los endpoints y operaciones validan
  `instance_id`; las capabilities son únicas por proceso.
- **Body malicioso o JSON enorme:** límites de bytes antes de decodificar,
  validación UTF-8 y rechazo explícito.
- **Watcher lento:** cola acotada, error `CONTROL_BACKPRESSURE` y cierre; nunca
  se descarta silenciosamente.
- **Replay omitido:** cursor exclusivo, `CURSOR_EXPIRED` y reintento desde el
  último cursor confirmado.
- **Descriptor stale:** comprobación de PID, health y TTL antes de borrar.
- **Acceso remoto accidental:** bind exclusivo a loopback, sin Tailscale, sin
  redirects y sin CORS.
- **Secretos en cuerpos:** no se escriben a logs ni al descriptor; solo pasan
  por RAM y por el canal autorizado del mensaje.

## 17. Autorrevisión de consistencia

La especificación mantiene las restricciones del proyecto:

- Una invocación crea una instancia nueva; varias invocaciones concurrentes se
  distinguen por `instance_id`.
- El descriptor de descubrimiento no es historial persistente y se elimina al
  cerrar; su metadata stale solo existe para limpiar crashes.
- La TUI humana continúa activa y comparte el EventHub con `ctl`.
- Los agentes pueden leer y enviar texto sin abrir la TUI.
- Mac sigue siendo servidor y Windows cliente.
- Tailscale solo transporta el tráfico Mac↔Windows.
- No se añade MCP ni una dependencia de proyecto/cwd compartida entre hosts.
- Cerrar o perder el proceso destruye mensajes, colas, presencia, capabilities
  y journal; no hay promesa de recuperación posterior.
