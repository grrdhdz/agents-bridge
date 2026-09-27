# Especificación: agentes locales en el mismo dispositivo

Estado: aprobada (2026-09-27)

Fecha: 2026-09-27

Base: especificación `2026-09-13-non-graphical-control-design.md` (v0.2.0) y el
trabajo sin commit de `EventHub` e `internal/control`.

Versión objetivo: `v0.2.0` (esta especificación amplía y corrige la de
2026-09-13; donde se contradicen, gana esta).

## 1. Propósito

Permitir que dos agentes en el mismo equipo se comuniquen por `codex-bridge`
sin Tailscale ni TUI. Caso principal: Claude Code como orquestador y un agente
de la app Codex (por ejemplo "Luna") como ejecutor.

Criterio de éxito: con un solo comando de arranque, el orquestador envía una
tarea, el ejecutor la recibe con `ctl wait`, responde con `ctl send`, y el
orquestador ve la respuesta y su estado `delivered`, sin copiar tokens ni
depender de Tailscale.

## 2. No objetivos

- No se entregan mensajes empujándolos a la app Codex (`codex queue`). La app
  usa un `app-server` privado por stdio y no hay daemon compartido; se evaluará
  por separado si eso cambia.
- No se añade MCP en esta versión.
- No cambia el protocolo TCP v1 ni sus valores de rol en el cable.
- No hay persistencia de mensajes; todo sigue en RAM.
- La ACL de Windows del descriptor sigue pendiente como en la spec de
  2026-09-13 (§5.1); esta spec no la resuelve.

## 3. Arquitectura

    Claude ──ctl --role orchestrator──► ControlHTTP(orq) ─ Client(orq) ─┐
                                                                         ├─ Server 127.0.0.1
    Luna   ──ctl --role executor────► ControlHTTP(ejec) ─ Client(ejec) ─┘
                          └──────────── un solo proceso: codex-bridge local ───────────┘

`codex-bridge local` es un proceso sin TUI que:

1. Crea un `Server` enlazado a `127.0.0.1:0` (sin detectar Tailscale).
2. Conecta dentro del mismo proceso un `Client` orquestador (owner token) y un
   `Client` ejecutor (pairing token), ambos por TCP loopback con el protocolo v1
   sin cambios.
3. Inicia un `ControlHTTP` y publica un descriptor por cada `Client`. Ambos
   descriptores comparten `instance_id` y difieren en `local_role`.
4. Imprime en stdout una única línea JSONL de arranque y queda en ejecución:

       {"v":1,"type":"ready","instance_id":"INSTANCE_ID","mode":"local"}

   No imprime tokens, capabilities ni `control_url`.
5. Termina con SIGINT/SIGTERM: cierra endpoints, borra descriptores y cierra
   clientes y servidor. No hay recuperación después del cierre.

Los modos existentes también publican descriptor: el orquestador Mac con TUI
(rol orquestador) y `join` (rol ejecutor). La TUI sigue funcionando igual.

## 4. Roles en la CLI

El cable conserva `mac-orchestrator` y `win-executor`. La CLI acepta alias:

| `--role` | Valor en el cable |
|---|---|
| `orchestrator` | `mac-orchestrator` |
| `executor` | `win-executor` |

El descriptor mantiene el valor del cable en `local_role`. Los mensajes de la
TUI y de la documentación dejan de asumir "Mac" y "Windows" en modo local.

## 5. Selección de instancia

Orden de filtros sobre los descriptores vivos del usuario:

1. `--instance-id`, si se indica.
2. `--role`, si se indica.
3. Igualdad de `cwd`, solo si no se indicó `--instance-id`.

Con cero coincidencias: `INSTANCE_NOT_FOUND` (salida 3). Con más de una:
`INSTANCE_AMBIGUOUS` (salida 2). En modo local ambos descriptores comparten
`cwd` e `instance_id`, así que sin `--role` la selección siempre es ambigua;
la skill indica usar siempre `--role`.

## 6. Comando `ctl wait`

    codex-bridge ctl wait --role ROLE [--timeout DURACION] [--format jsonl|text]

Devuelve el siguiente mensaje **del otro rol** que este rol aún no consumió, y
lo confirma. Los mensajes propios, deliveries y estados no cuentan.

### 6.1 Cursor de consumo

- Cada `ControlHTTP` guarda en RAM un cursor de consumo (`consumed_event_seq`).
  El agente no necesita recordar cursores.
- `wait` busca el primer evento `message` con remitente del otro rol y
  `event_seq > consumed_event_seq`; si no existe, se suscribe al `EventHub` y
  espera.
- Tras escribir la respuesta completa, el servidor llama a `Client.Ack` y
  avanza el cursor. Si la escritura falla o la conexión se cancela antes, no hay
  ACK ni avance: el mensaje se volverá a entregar (at-least-once).
- Si el siguiente mensaje sin consumir fue expulsado del journal (límites de
  1.000 mensajes u 8 MiB), `wait` devuelve `CURSOR_EXPIRED` con
  `oldest_event_seq` (salida 7) y adelanta el cursor a ese punto, para que el
  hueco se informe una vez y el siguiente `wait` continúe.
- Solo puede haber un `wait` activo por endpoint. Un segundo recibe
  `WAIT_IN_PROGRESS` (HTTP 409, salida 5). Cancelar la conexión libera el turno.

### 6.2 Endpoint

    POST /v1/wait?timeout_ms=N

Es `POST` porque avanza el cursor. `timeout_ms` por defecto 300000 (5 min),
máximo 1800000 (30 min). Mismos requisitos de autenticación y
`X-Codex-Bridge-Request-ID` que el resto.

### 6.3 Salida

Mensaje recibido (salida 0):

    {"v":1,"type":"response","ok":true,"operation":"wait","status":"message","instance_id":"ID","event_seq":29,"message":{...envelope...}}

Tiempo agotado sin mensaje (salida 0):

    {"v":1,"type":"response","ok":true,"operation":"wait","status":"timeout","instance_id":"ID"}

Instancia cerrada durante la espera: error `INSTANCE_CLOSED` (salida 3).

Con `--format text` la salida es legible para un modelo, con el cuerpo sin
escapar:

    --- codex-bridge message_id=ID from=orchestrator event_seq=29
    <cuerpo UTF-8 intacto>

y para timeout una sola línea `--- codex-bridge timeout`. Los errores siempre
se imprimen en JSONL en stderr.

## 7. Cambios en `ctl send`

    codex-bridge ctl send --role ROLE [--message-id ID] --body-file FILE|-

- `--message-id` pasa a ser opcional. Si se omite, `ctl` genera uno y lo
  devuelve en la respuesta; el reintento idempotente solo es posible si el
  agente reutiliza ese ID.
- Resto del contrato sin cambios (spec 2026-09-13 §7.7).

## 8. Correcciones previas obligatorias

1. **Canal `events` eliminado.** `Client.emit` deja de escribir en
   `c.events`; se eliminan `Client.Events()`, `waitForFrame` y `frameMsg`. El
   `EventHub` es la única fuente de eventos. Evita el bloqueo del lector TCP
   cuando el búfer de ~1288 frames se llena sin lector.
2. **ACK según el consumidor.** La TUI sigue confirmando al mostrar el mensaje.
   Los clientes sin TUI confirman solo cuando `wait` entrega el mensaje (§6.1).
   `read` y `watch` no confirman. Así `delivered` significa que el agente lo
   recibió, no solo que llegó al proceso.
3. **Idempotencia atómica.** `PublishWithID` comprueba y registra el
   `message_id` bajo un mismo mutex, para que dos envíos simultáneos con el
   mismo ID no se publiquen ambos.
4. **Descriptor.** En Unix la ruta usa `os.Getuid()`, no `$USER`. Si el
   directorio ya existe, se verifica que sea del usuario y tenga modo `0700`;
   si no, `ctl` queda deshabilitado con un error accionable.
5. **`ctl` conectado a `main`.** Subcomandos `list`, `read`, `watch`, `send` y
   `wait`, más `local`. Todos los modos publican descriptor.

## 9. Skill compartida `codex-bridge`

Se crea `.agents/skills/codex-bridge/SKILL.md` en este repositorio. Explica:

- **Orquestador:** arrancar `codex-bridge local` en segundo plano, enviar
  tareas con `ctl send --role orchestrator`, esperar con `ctl wait --role
  orchestrator` en segundo plano y cerrar el proceso al terminar.
- **Ejecutor:** bucle `ctl wait --role executor --format text`; al recibir un
  mensaje, hacer el trabajo, responder con `ctl send --role executor` y volver a
  esperar. No terminar el turno mientras no llegue `FIN` o la instancia se
  cierre.
- **Convención de mensajes:** la primera línea del cuerpo es una etiqueta:
  `TAREA`, `PREGUNTA`, `RESPUESTA`, `RESULTADO` o `FIN`.
- **Errores:** salida 3 → la instancia ya no existe, el ejecutor termina e
  informa; salida 5 con `WAIT_IN_PROGRESS` → hay otro `wait` activo; salida 8 →
  reintentar.

La frase para activar a Luna es: "usa la skill codex-bridge como ejecutor".

## 10. Riesgos

- **Sandbox de Codex.** En `workspace-write`, el sandbox de Codex en macOS
  puede bloquear la red, incluido loopback. Si `ctl` no puede conectar desde
  Luna, habrá que habilitar `network_access` para esa sesión o aprobar el
  comando. Es el primer punto de la verificación manual (§11).
- **Fin de turno del ejecutor.** Un agente puede cerrar su turno y dejar de
  esperar. La skill lo mitiga con instrucciones explícitas; no hay garantía
  técnica. El orquestador lo detecta porque sus mensajes quedan en `accepted`
  sin pasar a `delivered`.
- **Timeouts de la shell del agente.** Si la herramienta de shell corta el
  comando antes de `--timeout`, la conexión se cancela sin ACK y el mensaje se
  reentrega en el siguiente `wait`. No se pierde nada.

## 11. Pruebas

Automatizadas (TDD, antes de cada componente):

1. `local`: arranca sin Tailscale, publica dos descriptores con el mismo
   `instance_id` y roles distintos, imprime solo la línea `ready`, y al recibir
   la señal borra ambos descriptores.
2. Selección: `--role` desambigua; sin `--role` en modo local da
   `INSTANCE_AMBIGUOUS`; `--instance-id` + `--role` funciona.
3. `wait`: devuelve el mensaje del otro rol e ignora los propios; confirma
   (el emisor ve `delivered`); timeout devuelve `status:"timeout"`; cancelar la
   conexión antes de escribir no confirma y el siguiente `wait` lo reentrega;
   un segundo `wait` concurrente da `WAIT_IN_PROGRESS`; cierre da
   `INSTANCE_CLOSED`.
4. `--format text`: cuerpo multilínea intacto.
5. `send` sin `--message-id` genera y devuelve un ID.
6. Regresión del canal eliminado: más de 1288 frames sin lector no bloquean al
   cliente.
7. Idempotencia concurrente: N envíos simultáneos con el mismo ID producen un
   solo envelope.
8. Descriptor: directorio con permisos incorrectos se rechaza.
9. TUI: sigue mostrando, confirmando y desplazando igual que en v0.1.3.

Verificación final: `go vet ./...`, `go test -race ./...`, builds darwin/arm64
y windows/amd64.

Verificación manual de extremo a extremo: Claude arranca `local`, envía
`TAREA`, Luna (app Codex) usa la skill, responde `RESULTADO`, Claude ve
`delivered` y la respuesta. Se anota si el sandbox de Codex requirió ajustes.

## 12. Archivos afectados

    cmd/codex-bridge/main.go            subcomandos local y ctl
    cmd/codex-bridge/ctl.go             cliente ctl, selección y códigos de salida
    internal/bridge/client.go           sin canal events; idempotencia atómica
    internal/bridge/server.go           sin cambios de protocolo
    internal/control/http.go            /v1/wait y cursor de consumo
    internal/control/descriptor.go      selección por rol; UID y permisos
    internal/tui/model.go               solo EventHub; ACK al mostrar
    .agents/skills/codex-bridge/SKILL.md
    README.md                           modo local y ctl

## 13. Enmienda (2026-09-27): instance_id obligatorio

Motivación: el usuario puede tener varios puentes a la vez, uno por proyecto,
cada uno con su propio chat de Codex. El `instance_id` debe acompañar siempre
a comandos, mensajes y activación; nunca se deduce de la instancia actual ni
del directorio de trabajo. Esta enmienda deja sin efecto el §5 (Selección de
instancia) en la parte que usaba `cwd` como filtro, y sustituye ese apartado
por lo siguiente.

**Selección de instancia (reemplaza el §5).** `SelectDescriptor` ya no recibe
ni usa `cwd`; filtra únicamente por `instance_id` (obligatorio en el llamador)
y, si se indica, por `--role`:

1. Con cero descriptores para ese `instance_id` (con el rol pedido, si lo
   hay): `INSTANCE_NOT_FOUND` (salida 3).
2. Con exactamente uno: se usa.
3. Con más de uno (mismo `instance_id`, sin `--role`, y ambos roles
   publicados): `INSTANCE_AMBIGUOUS` (salida 2).

`ctlEnv` pierde el campo `cwd` y `main.go` deja de llamar a `os.Getwd()` para
`ctl` (lo sigue haciendo para `local`, que solo usa `cwd` como metadato
informativo del descriptor, no para seleccionarlo).

1. **`ctl` exige `--instance-id`.** `read`, `watch`, `send` y `wait` fallan
   con `USAGE` (salida 2) y el mensaje `--instance-id is required` si se
   omite, antes de intentar ninguna selección. `ctl list` no cambia: sigue sin
   necesitarlo, porque lista todas las instancias vivas del usuario.

2. **Endpoint inalcanzable es un caso distinto de instancia cerrada.** En
   `request()` (`cmd/codex-bridge/ctl.go`), si `control.Do` falla y el
   contexto no está cancelado, el error ya no es `INSTANCE_NOT_FOUND`: es un
   código nuevo, `CONTROL_UNREACHABLE` (salida 8), con un mensaje que indica
   que puede deberse a un sandbox sin acceso a loopback (p. ej. Codex en
   `workspace-write`) y que el comando debe repetirse fuera de él. Esto separa
   dos situaciones que antes compartían salida 3: el descriptor ya no existe
   (`INSTANCE_NOT_FOUND`/`INSTANCE_CLOSED`, el puente terminó de verdad) frente
   a un descriptor que sigue vivo pero el proceso actual no puede alcanzar su
   `control_url` loopback (`CONTROL_UNREACHABLE`, reintentar fuera del
   sandbox).

3. **Cabecera de `ctl wait --format text` incluye la instancia.** Para no
   confundir la salida de dos puentes distintos en una misma terminal o log:

       --- codex-bridge instance=<instance_id> message_id=<id> from=<rol> event_seq=<n>
       <cuerpo>

   y para timeout:

       --- codex-bridge instance=<instance_id> timeout

4. **`codex open` exige `--instance-id`.** Falla con `USAGE` (salida 2) si se
   omite. Con `--instance-id` presente, valida con
   `control.SelectDescriptor(root, id, protocol.RoleExecutor)` que existe una
   instancia viva con rol ejecutor bajo ese id; si no, `INSTANCE_NOT_FOUND`
   (salida 3) — igual que si el ejecutor nunca se hubiera levantado.
   `codexEnv` gana un campo `root` (el mismo mecanismo de override de
   directorio de descriptores que ya usan `ctlEnv` y `runLocal`; cadena vacía
   en producción). El prompt escrito en el deeplink es siempre la línea
   `usa la skill codex-bridge como ejecutor con --instance-id <id>`; con
   `--prompt-file`, su contenido se añade después de una línea en blanco. El
   límite de 8 KiB (`maxCodexOpenPromptBytes`) se aplica al prompt combinado,
   no solo al archivo.

Pruebas añadidas (TDD, cada una se confirmó en rojo contra el código sin la
enmienda antes de implementarla): selección sin `cwd`
(`internal/control/control_test.go`); `--instance-id` ausente en `read`,
`watch`, `send` y `wait` (`cmd/codex-bridge/local_test.go`);
`CONTROL_UNREACHABLE` con un descriptor vivo cuyo `control_url` apunta a un
puerto loopback cerrado; cabecera de texto con `instance=<id>` en mensaje y en
timeout; `codex open` sin `--instance-id` (uso), con instancia inexistente
(`INSTANCE_NOT_FOUND`) y con prompt que contiene el `instance_id`
(`cmd/codex-bridge/codex_test.go`).

Verificación: `gofmt -l .` vacío, `go vet ./...`, `go test -race -count=1
./...`, y builds darwin/arm64 y `GOOS=windows GOARCH=amd64` sobre
`./cmd/codex-bridge`.
