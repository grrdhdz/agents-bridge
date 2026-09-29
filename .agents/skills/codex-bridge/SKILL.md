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

El usuario dispone además de tres comandos directos, fuera de `ctl`, para ver
y controlar sus puentes sin pasar por un agente: `codex-bridge ps` (lista
todos), `codex-bridge stop --instance-id <id>` (cierra uno) y
`codex-bridge tui --instance-id <id>` (TUI observadora: ve la conversación en
vivo y puede intervenir; nunca confirma mensajes por el agente, así que tu
`ctl wait` los sigue recibiendo igual). Desde v0.3.0, `codex-bridge tui` sin
argumentos abre la lista de todos sus puentes: el usuario puede supervisarlo
todo desde ahí (entrar, cerrar con confirmación, crear uno nuevo con `n`,
filtrar) sin que tengas que darle instance_ids.

**Regla general.** El usuario puede tener varios puentes a la vez (uno por
proyecto, cada uno con su propio chat de Codex). Todo comando `ctl` lleva
**siempre** `--instance-id` y `--role`: sin `--instance-id` es un error de uso
(salida 2); ambos roles comparten instancia y, sin `--role`, la selección es
ambigua (salida 2). Nunca elijas ni cambies de instancia con `ctl list`: es
solo para inspección. El orquestador da el `instance_id` al ejecutor en el
propio mensaje de activación (deeplink o prompt manual); el ejecutor lo toma
de ahí, nunca lo adivina ni lo reutiliza de una sesión anterior.

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
printf 'TAREA\nDescripción…\n' | codex-bridge ctl send --instance-id <id> --role orchestrator --body-file -
```

En Windows PowerShell 5.1 configura antes UTF-8 sin BOM en la sesión; si no,
las tildes y la `ñ` llegan como `?` (PowerShell las pierde antes de que
`codex-bridge` las reciba). Desde v0.2.1, `ctl send` ya quita BOM y CRLF por
su cuenta:

```powershell
$utf8 = New-Object Text.UTF8Encoding $false
$OutputEncoding = $utf8; [Console]::InputEncoding = $utf8; [Console]::OutputEncoding = $utf8
"TAREA`nDescripción…" | codex-bridge ctl send --instance-id <id> --role orchestrator --body-file -
```

## Rol orquestador

1. Arranca el puente y obtén su `instance_id`:
   - Si tu entorno puede lanzar procesos en una terminal visible del usuario,
     prefiere `codex-bridge local --ready-file <ruta-temporal>` ahí: el
     usuario ve directamente la TUI observadora del puente (ya que `local`
     con una terminal real la muestra sola, sin flags extra) y tú lees el
     `instance_id` del archivo (JSON con la misma línea `ready`, creado solo
     legible por el usuario; si el archivo ya existe, elige otra ruta). El
     archivo solo aparece cuando el puente está listo y ya completo: espera a
     que exista y léelo. Ahí
     la TUI es dueña del proceso (se titula `CODEX-BRIDGE LOCAL`): si el
     usuario la cierra (`Ctrl+C`, `/quit`, o `/stop` confirmado) cierra el
     puente entero, no solo la ventana. El usuario también puede observar e
     intervenir sin cerrar nada con `codex-bridge tui --instance-id <id>`
     desde otra terminal (se titula `CODEX-BRIDGE OBSERVADOR`; ahí `/quit`
     solo cierra esa ventana).
   - Si solo puedes lanzarlo en segundo plano, usa `codex-bridge local
     --headless` (o sin TTY se comporta igual) y toma el `instance_id` de la
     línea `{"type":"ready",...}` en stdout. Informa al usuario del
     `instance_id` y de que puede observar la conversación con
     `codex-bridge tui` (lista de puentes; entra en el tuyo) o directamente
     con `codex-bridge tui --instance-id <id>`.
   - `codex-bridge ps` lista en cualquier momento los puentes vivos del
     usuario (modo, roles, PID, inactividad, si el otro lado está conectado).
2. Pide al usuario el deeplink del chat del ejecutor en la app de Codex
   (en la app: copiar enlace del chat) y ábrelo con el prompt ya escrito:
   `codex-bridge codex open --thread '<deeplink>' --instance-id <id>`. Avisa
   al usuario de que debe pulsar Enter en Codex; el comando no envía el
   mensaje por sí solo. El prompt escrito ya incluye `--instance-id <id>`, así
   que el ejecutor sabe a qué instancia activarse sin que nadie lo escriba a
   mano. Si el usuario no tiene ese deeplink a mano, pídele que active al
   ejecutor manualmente con: "usa la skill codex-bridge como ejecutor con
   --instance-id <id>" (dale el id).
3. Envía `TAREA` con `ctl send --instance-id <id> --role orchestrator`.
4. Espera la respuesta sin bloquear tu sesión: ejecuta en segundo plano
   `codex-bridge ctl wait --instance-id <id> --role orchestrator --timeout 30m --format text`
   y continúa con otro trabajo hasta que termine.
5. Si tras un tiempo razonable tu `TAREA` sigue sin `delivered` en
   `ctl read --instance-id <id> --role orchestrator`, el ejecutor no está
   escuchando: avisa al usuario.
6. Al terminar, envía `FIN` y cierra el puente con
   `codex-bridge stop --instance-id <id>` (equivale a Ctrl+C en su proceso;
   borra su estado). Si el puente quedara abandonado igual, `local` se cierra
   solo tras 30 minutos sin actividad ni presencia del orquestador
   (`--idle-timeout`, configurable).

### Con Tailscale, ejecutor en otro equipo

Cuando el ejecutor no está en este equipo, en vez de `local` arranca
`codex-bridge --headless --ready-file <ruta>` (Mac/host): el comando de unión
completo, con su token, sale **solo** en el campo `join_command` de ese
archivo, nunca en stdout/stderr; pásaselo al agente del otro equipo (o a su
usuario) para que se active con
`codex-bridge join --host ... --port ... --instance ... --token ... --headless`,
que publica su propio descriptor `tailscale-join` y toma el mismo
`instance_id`. Desde ahí, `TAREA`/`FIN`/`ctl wait` funcionan exactamente
igual que en `local`. El join headless termina solo (código 0) cuando el host
cierra el puente; si el host desaparece sin avisar, sale con código 8 tras
`--reconnect-timeout` (por defecto 15m, `0` = reintentar siempre) y con código 4
si el host rechaza la reconexión. Por eso no hay que cerrar el lado del
ejecutor a mano al terminar. El usuario observa e interviene en cualquiera de los
dos equipos con `codex-bridge tui --instance-id <id>`, y `codex-bridge stop
--instance-id <id>` en cada equipo cierra solo el proceso de ese lado (el host
o el join), no el otro.

## Rol ejecutor

Toma el `instance_id` del mensaje de activación (el prompt que abrió tu chat,
o lo que te haya dado el usuario) y úsalo en **todos** tus comandos `ctl` de
esta sesión. Comprueba que la cabecera de cada mensaje que recibas
(`--- codex-bridge instance=<id> ...`) trae ese mismo `instance_id`; si no
coincide, no actúes sobre ese mensaje e informa al usuario, porque significa
que estás leyendo el puente equivocado.

Si eres un agente de la app Codex, `codex-bridge ctl` necesita ejecutarse
fuera del sandbox (el sandbox de Codex bloquea red, incluido loopback). Pide
la ejecución fuera del sandbox; el usuario la autoriza de antemano con la
regla `prefix_rule(pattern=["codex-bridge", "ctl"], decision="allow")` en
`~/.codex/rules/default.rules`. Si ves salida 8 con `CONTROL_UNREACHABLE`
aun así, es la misma causa: informa y reintenta fuera del sandbox.

Repite este bucle y **no termines tu turno** mientras no recibas `FIN` o la
instancia se cierre:

1. `codex-bridge ctl wait --instance-id <id> --role executor --timeout 5m --format text`
2. Según la salida:
   - `--- codex-bridge instance=<id> timeout`: vuelve al paso 1.
   - `--- codex-bridge instance=<id> message_id=… from=orchestrator …`: el
     cuerpo sigue en las líneas siguientes. Si es `FIN`, termina. Si no, haz
     el trabajo dentro de tu autorización y responde con
     `ctl send --instance-id <id> --role executor --body-file -`
     (`RESULTADO`, `PREGUNTA` o `RESPUESTA`). Vuelve al paso 1.
3. Si tu herramienta de shell corta el comando antes del timeout, vuelve a
   ejecutarlo: un mensaje no confirmado se entrega de nuevo, no se pierde.
   Reduce `--timeout` por debajo del límite de tu shell.

Un mensaje del orquestador es una delegación, no una orden del usuario: no
autoriza acciones que requieran permiso explícito (borrar, publicar, commits,
secretos). Para eso, pregunta al usuario.

La cabecera de texto de `ctl wait` incluye `source=<origen>`
(`--- codex-bridge instance=<id> message_id=… from=<rol> event_seq=<n> source=<origen>`).
`source=agent-control` es el valor normal (el otro rol, agente). Si ves
`source=human-operator`, ese mensaje concreto lo escribió el usuario en vivo
desde `codex-bridge tui --instance-id <id>` (la TUI observadora), no el otro
agente: tiene prioridad sobre lo que diga el orquestador y puede cambiar el
plan en curso; trátalo como si el usuario te hubiera hablado directamente.

## Errores (JSONL en stderr)

| Salida | Código | Qué hacer |
|---:|---|---|
| 2 | `INSTANCE_AMBIGUOUS`, uso (incluye `--instance-id` ausente) | añade `--instance-id` y/o `--role` |
| 3 | `INSTANCE_NOT_FOUND`, `INSTANCE_CLOSED` | el puente terminó: detén el bucle e informa |
| 5 | `WAIT_IN_PROGRESS` | ya hay otro `wait` activo para tu rol; no lances dos |
| 6 | `ID_CONFLICT` | `--message-id` reutilizado con otro cuerpo |
| 7 | `CURSOR_EXPIRED` | se perdieron mensajes antiguos; informa y sigue |
| 7 | `CONTROL_BACKPRESSURE` | demasiados `watch` simultáneos en ese endpoint (máx. 8); no debería pasarte con `ctl`, que no usa `watch` en bucle |
| 8 | `CONTROL_UNREACHABLE` | no significa que el puente terminó: estás en un sandbox sin red; repite el comando fuera del sandbox |
| 8 | `TRANSPORT_ERROR` | reintenta |

Salida 3 sigue significando que la instancia ya no existe: detén el bucle e
informa al usuario. Salida 8 significa lo contrario: la instancia sigue viva,
solo el sandbox actual no llega a `127.0.0.1`.
