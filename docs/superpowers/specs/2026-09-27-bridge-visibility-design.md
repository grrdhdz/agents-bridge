# Especificación: visibilidad y control humano de los puentes

> Documento histórico anterior al renombre de 2026-10-01. Las referencias a
> `codex-bridge` describen esa versión; para el uso actual consulta la sección
> «Migración desde codex-bridge» del [README](../../../README.md).

Estado: aprobada e implementada (2026-09-27)

Fecha: 2026-09-27

Base: `2026-09-27-local-agents-design.md` (incluida la enmienda §13:
`instance_id` obligatorio) y el código de `feat/local-agents` (`fe930be`).

## 1. Propósito

El usuario debe poder, en todo momento:

1. Saber qué puentes están vivos en su equipo y cerrarlos.
2. Ver la conversación entre agentes en tiempo real e intervenir.
3. No acumular procesos huérfanos.

Y el mismo modelo debe servir cuando la comunicación vuelva a ser por
Tailscale entre un agente local y otro en otro dispositivo.

Criterio de éxito: con `codex-bridge ps` el usuario ve todos sus puentes; con
`codex-bridge tui --instance-id ID` sigue e interviene en cualquiera; con
`codex-bridge stop --instance-id ID` lo cierra; y un puente abandonado se
cierra solo.

## 2. No objetivos

- No hay daemon global ni registro persistente: cada puente sigue siendo un
  proceso aislado y todo vive en RAM. `ps` solo lee descriptores.
- No cambia el protocolo TCP v1 ni sus frames.
- No hay acceso remoto al plano de control: `ps`, `stop` y `tui` actúan sobre
  los puentes del mismo equipo y usuario. En Tailscale, cada lado observa su
  propio proceso.
- No se prueba en Windows real en esta versión; se verifica compilación y
  pruebas sobre loopback.

## 3. Modo del puente en el descriptor

El descriptor gana un campo `mode` (compatible: lectores antiguos lo
ignoran; `descriptor_version` sigue en 1):

| `mode` | Proceso |
|---|---|
| `local` | `codex-bridge local` (ambos roles) |
| `tailscale-host` | `codex-bridge` en Mac (servidor + orquestador) |
| `tailscale-join` | `codex-bridge join` (ejecutor remoto) |

Y un campo `last_activity_at` que el heartbeat del descriptor actualiza
(§5.2).

## 4. `ps` y `stop`

### 4.1 `codex-bridge ps [--format table|jsonl]`

Agrupa los descriptores vivos por `instance_id` (tras la limpieza de stale ya
existente) y muestra, por defecto como tabla legible:

    INSTANCE                     MODE            ROLES                 PID    STARTED   IDLE   PEER  MSGS
    7qc5h3vmxj6lcebmfg6anrklde   local           orchestrator,executor 11899  12:20     3m     sí    4

- `IDLE`: tiempo desde `last_activity_at`.
- `PEER`: si el otro rol está conectado. Hoy `peer_connected` de `/v1/health`
  informa por error si el **propio** cliente está conectado; se corrige: en
  `local`, ambos clientes conectados; en `tailscale-host`, el servidor tiene un
  ejecutor unido (nuevo método del `Server`); en `tailscale-join`, el propio
  cliente conectado al Mac (lo único observable desde ese lado).
- `MSGS`: `latest_server_seq`.
- `--format jsonl`: un registro `{"v":1,"type":"response","operation":"ps","instances":[...]}`.
- Nunca muestra capability, `control_url`, tokens ni `cwd`.

### 4.2 `codex-bridge stop --instance-id ID`

Cierra ese puente de forma limpia (equivale a Ctrl+C en su proceso):

- Nuevo endpoint `POST /v1/stop` (mismo auth y request-id que el resto).
  Responde `202 {"operation":"stop","status":"stopping"}` y el proceso inicia
  su cierre normal: borra descriptores, cierra clientes y servidor.
- Solo lo aceptan el endpoint del orquestador en `local` y `tailscale-host`, y
  el del ejecutor en `tailscale-join` (que cierra únicamente el proceso join;
  la instancia del Mac sigue viva). El endpoint del ejecutor en `local`
  responde `FORBIDDEN` (403, salida 5): un agente ejecutor no puede cerrar el
  puente.
- `stop` elige ese endpoint automáticamente; si la instancia no existe,
  `INSTANCE_NOT_FOUND` (3); si el endpoint no responde, `CONTROL_UNREACHABLE`
  (8) con el PID en el mensaje para que el usuario pueda usar `kill`.
- Salida: `{"v":1,"type":"response","ok":true,"operation":"stop","instance_id":"ID"}`.

Esto enmienda la spec de 2026-09-13 (§2), que reservaba el cierre a la TUI del
orquestador: ahora también puede hacerlo el usuario desde `stop` y la TUI
observadora.

## 5. Protección contra huérfanos

### 5.1 `--idle-timeout` en `local`

`codex-bridge local --idle-timeout DURACION` (por defecto `30m`; `0`
desactiva). Si durante ese tiempo no hay **actividad**, el proceso se cierra
como con `stop`.

**Actividad** es cualquiera de:

- un mensaje publicado o recibido por cualquiera de los dos clientes;
- una conexión abierta del lado orquestador: un `wait` o `watch` en curso en
  el endpoint del orquestador, o una TUI observadora conectada (§6).

Los `wait` del ejecutor **no** cuentan: si el orquestador desaparece, el
ejecutor seguiría esperando para siempre. Así, un puente vive mientras su
orquestador (agente o humano) esté presente o haya conversación, y se cierra
30 minutos después de que ambos cesen. El ejecutor recibe entonces salida 3 y
termina, como indica la skill.

### 5.2 `last_activity_at`

El heartbeat del descriptor (cada 3 s) escribe `last_activity_at` con la
última actividad según §5.1, para que `ps` muestre `IDLE`.

## 6. TUI observadora: `codex-bridge tui --instance-id ID`

Se conecta a un puente vivo **a través del plano de control**, no del
protocolo TCP. Así funciona igual para `local`, `tailscale-host` y
`tailscale-join`.

- **Endpoint usado:** el del orquestador si existe en este equipo; si no (lado
  join), el del ejecutor.
- **Qué muestra:** replay completo desde `event_seq` 0 vía `/v1/watch` y luego
  eventos en vivo: mensajes de ambos roles con su rol, hora, estado
  (`queued`, `accepted`, `delivered`) y origen (agente o humano). Mismo estilo
  de la TUI actual: propios a la derecha, del otro rol a la izquierda,
  desplazamiento con rueda/PgUp/PgDn.
- **No consume:** usa `watch`, que no confirma ni avanza el cursor de `wait`.
  El agente sigue recibiendo todos los mensajes. Pueden conectarse varias
  TUIs. El límite de 8 `watch` simultáneos por endpoint de la spec
  2026-09-13 (§8) no está implementado; se implementa aquí (el noveno recibe
  `CONTROL_BACKPRESSURE`, 429, salida 7).
- **Intervención:** lo que el usuario escribe (Ctrl+S / Ctrl+Enter) se envía
  con `/v1/send` desde el rol del endpoint, con `source: "human-operator"`.
- **Comandos:** `/stop` (con confirmación) llama a `/v1/stop`; `/quit` o
  Ctrl+C cierra solo la TUI, el puente sigue vivo.
- **Reconexión:** si el `watch` se corta, reintenta desde el último `event_seq`
  mostrado; si la instancia desaparece, lo indica y se cierra.

### 6.1 Origen de los mensajes

`/v1/send` acepta un campo opcional `source`:

- `agent-control` (por defecto para `ctl send`);
- `human-operator` (TUI observadora).

El envelope ya tiene `source` y el servidor no lo valida, así que no cambia el
protocolo. La TUI humana existente sigue usando `manual-codex-copy`, que se
muestra también como humano. La cabecera de `ctl wait --format text` añade
`source=<source>`, y la skill indica al agente que un mensaje
`human-operator` viene del usuario y tiene prioridad sobre el orquestador.

### 6.2 Reutilización de la TUI

`internal/tui` hoy depende de `*bridge.Client`. Se extrae una interfaz mínima
(suscripción a eventos, publicar, estado de conexión) con dos
implementaciones: el `Client` directo (TUI actual) y un cliente del plano de
control (observadora). La TUI actual conserva su comportamiento.

## 7. Arranque visible

### 7.1 `local` con terminal

- Si stdout es una terminal, `codex-bridge local` muestra directamente la TUI
  observadora de su propia instancia (conectada al orquestador), en lugar de
  quedarse en silencio. Cerrarla con Ctrl+C o `/stop` cierra el puente.
- Con `--headless`, o si stdout no es una terminal, se comporta como hoy:
  imprime la línea `ready` y queda en segundo plano.
- `--ready-file PATH` escribe la línea `ready` en un archivo `0600` (creación
  exclusiva). Sirve para que un orquestador que lanza el puente en la
  terminal del usuario conozca su `instance_id` sin buscarlo con `ps`.

### 7.2 Skill

El orquestador, cuando su entorno lo permita, lanza `codex-bridge local
--ready-file ...` en la terminal visible del usuario (así el usuario ve la
TUI y puede cerrarla). Si solo puede lanzarlo en segundo plano, informa al
usuario del `instance_id` y de `codex-bridge tui --instance-id ID`. En ambos
casos, al terminar envía `FIN` y usa `codex-bridge stop --instance-id ID`.

## 8. Tailscale

- `codex-bridge join ... --headless`: sin TUI; publica el descriptor del
  ejecutor (`tailscale-join`), imprime `ready`, reconecta solo como `local`, y
  un agente en ese equipo usa `ctl --instance-id ID --role executor`. El
  usuario en ese equipo puede observar con `codex-bridge tui`.
- `codex-bridge` en Mac (`tailscale-host`) acepta `--headless` y
  `--ready-file`. En modo headless, el comando de unión (con su token) se
  escribe solo en el `--ready-file` (`0600`) como `join_command`, nunca en
  stdout; sin `--ready-file`, headless se rechaza.
- `--idle-timeout` también se acepta en `tailscale-host` (por defecto `0`,
  desactivado: el puente humano de Mac↔Windows no debe cerrarse solo).
- El `instance_id` es el mismo en ambos equipos; `ps` en cada uno muestra su
  propio proceso.

## 9. Errores nuevos

| Código | HTTP | Salida | Caso |
|---|---:|---:|---|
| `FORBIDDEN` | 403 | 5 | `stop` desde el endpoint del ejecutor en `local` |

`INSTANCE_NOT_FOUND` y `CONTROL_UNREACHABLE` se reutilizan.

## 10. Pruebas

1. Descriptor: `mode` y `last_activity_at` presentes; lectores toleran su
   ausencia.
2. `ps`: tabla y JSONL agrupan dos roles de una instancia; dos instancias
   aparecen por separado; no filtra secretos.
3. `peer_connected` según §4.1 en los tres modos; límite de 8 `watch`.
4. `stop`: cierra `local` y borra descriptores; desde el ejecutor en `local`
   da `FORBIDDEN`; instancia inexistente da 3.
5. Idle: con timeout corto, `local` se cierra sin actividad; un `wait` del
   orquestador en curso lo mantiene vivo; un `wait` del ejecutor no; un
   mensaje reinicia el reloj.
6. TUI observadora (modelo, sin terminal real): replay e historial en vivo de
   ambos roles; enviar usa `source=human-operator`; no confirma mensajes (el
   `wait` del agente sigue recibiéndolos); `/stop` llama a stop.
7. `source`: `ctl send` → `agent-control`; cabecera de texto lo incluye.
8. `local` sin TTY sigue imprimiendo `ready`; `--ready-file` escribe `0600` y
   falla si el archivo existe.
9. `join --headless` y `tailscale-host --headless` contra un servidor
   loopback: publican descriptor con su `mode`; headless sin `--ready-file`
   en host se rechaza; el token no sale por stdout.
10. Regresión: la TUI actual y `ctl` siguen igual.

Verificación final: `gofmt -l .`, `go vet ./...`, `go test -race ./...`,
builds darwin/arm64 y windows/amd64. Manual: `local` en la terminal del
usuario con TUI, conversación con Luna observada e intervenida, `ps` y `stop`.

## 11. Fases de implementación

1. `mode`, `last_activity_at`, `peer_connected` corregido, `ps`, `stop`,
   `--idle-timeout`.
2. `source`, límite de watchers, interfaz de la TUI, TUI observadora, `local` con TTY y
   `--ready-file`, skill.
3. `--headless` en `join` y en el host Mac.
