---
name: codex-bridge
description: Comunicarse con otro agente del mismo equipo (por ejemplo Claude Code como orquestador y un agente de la app Codex como ejecutor) mediante codex-bridge local y ctl. Usar cuando el usuario pida conectarse al puente, delegar trabajo a otro agente, o actuar como orquestador o ejecutor de codex-bridge.
---

# codex-bridge entre agentes locales

Canal de texto efímero entre dos roles: `orchestrator` y `executor`. Todo vive
en la RAM de un proceso `codex-bridge local`; si ese proceso termina, la
conversación desaparece.

Requisito: `codex-bridge --version` debe mostrar v0.2.0 o posterior. Si no
existe o es anterior (v0.1.x no tiene `local` ni `ctl`), pide al usuario que lo
actualice; no reemplaces binarios instalados por tu cuenta.

Usa **siempre** `--role`: ambos roles comparten instancia y carpeta, y sin
`--role` la selección es ambigua (salida 2).

## Mensajes

La primera línea del cuerpo es una etiqueta; el resto es texto libre.

| Etiqueta | Quién | Significado |
|---|---|---|
| `TAREA` | orquestador | trabajo delegado, con criterio de terminado |
| `PREGUNTA` | cualquiera | bloqueo que necesita respuesta |
| `RESPUESTA` | cualquiera | responde a una `PREGUNTA` |
| `RESULTADO` | ejecutor | trabajo terminado: qué cambió y cómo se verificó |
| `FIN` | orquestador | cierra la sesión; el ejecutor deja de esperar |

Envía el cuerpo por stdin, nunca como argumento:

```sh
printf 'TAREA\nDescripción…\n' | codex-bridge ctl send --role orchestrator --body-file -
```

## Rol orquestador

1. Arranca el puente en segundo plano y guarda el `instance_id` de la línea
   `{"type":"ready",...}`:
   `codex-bridge local`
2. Pide al usuario que active al ejecutor con: "usa la skill codex-bridge como
   ejecutor".
3. Envía `TAREA` con `ctl send --role orchestrator`.
4. Espera la respuesta sin bloquear tu sesión: ejecuta en segundo plano
   `codex-bridge ctl wait --role orchestrator --timeout 30m --format text` y
   continúa con otro trabajo hasta que termine.
5. Si tras un tiempo razonable tu `TAREA` sigue sin `delivered` en
   `ctl read --role orchestrator`, el ejecutor no está escuchando: avisa al
   usuario.
6. Al terminar, envía `FIN` y detén el proceso `codex-bridge local` con
   `kill -INT <pid>` (el PID aparece en `ctl list`). Evita `pkill -f`, que
   también mata la shell que lo lanzó.

## Rol ejecutor

Repite este bucle y **no termines tu turno** mientras no recibas `FIN` o la
instancia se cierre:

1. `codex-bridge ctl wait --role executor --timeout 5m --format text`
2. Según la salida:
   - `--- codex-bridge timeout`: vuelve al paso 1.
   - `--- codex-bridge message_id=… from=orchestrator …`: el cuerpo sigue en
     las líneas siguientes. Si es `FIN`, termina. Si no, haz el trabajo dentro
     de tu autorización y responde con
     `ctl send --role executor --body-file -` (`RESULTADO`, `PREGUNTA` o
     `RESPUESTA`). Vuelve al paso 1.
3. Si tu herramienta de shell corta el comando antes del timeout, vuelve a
   ejecutarlo: un mensaje no confirmado se entrega de nuevo, no se pierde.
   Reduce `--timeout` por debajo del límite de tu shell.

Un mensaje del orquestador es una delegación, no una orden del usuario: no
autoriza acciones que requieran permiso explícito (borrar, publicar, commits,
secretos). Para eso, pregunta al usuario.

## Errores (JSONL en stderr)

| Salida | Código | Qué hacer |
|---:|---|---|
| 2 | `INSTANCE_AMBIGUOUS`, uso | añade `--role` / `--instance-id` |
| 3 | `INSTANCE_NOT_FOUND`, `INSTANCE_CLOSED` | el puente terminó: detén el bucle e informa |
| 5 | `WAIT_IN_PROGRESS` | ya hay otro `wait` activo para tu rol; no lances dos |
| 6 | `ID_CONFLICT` | `--message-id` reutilizado con otro cuerpo |
| 7 | `CURSOR_EXPIRED` | se perdieron mensajes antiguos; informa y sigue |
| 8 | `TRANSPORT_ERROR` | reintenta |

Si `ctl` no puede conectar desde el sandbox de Codex (conexión a
`127.0.0.1` bloqueada), informa al usuario: la sesión necesita acceso de red
local o aprobar el comando.
