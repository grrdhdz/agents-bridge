# Plan: hooks de harness y renombre a agents-bridge (v0.5.0)

Estado: aprobado con decisiones del usuario (§9). Fecha: 2026-10-01. Base: `v0.4.0`.

## 1. Objetivo

1. Que la coordinación entre agentes no dependa solo de que el modelo obedezca
   la skill: el harness (Claude Code, Codex) ejecuta código nuestro en momentos
   fijos mediante sus hooks.
2. Renombrar la herramienta a `agents-bridge`, porque ya no está atada a Codex.

Criterio de éxito: un ejecutor vinculado a un puente no puede terminar su turno
sin `FIN`, recibe un `URGENTE` mientras trabaja (no al terminar), aparece como
"trabajando" mientras usa herramientas y el puente no se cierra por inactividad
durante ese trabajo; todo sin que el modelo tenga que acordarse.

## 1.1 Convivencia con proyectos en v0.4 (obligatorio)

Otro proyecto usa `codex-bridge` v0.4 en este equipo. Prohibido matar,
interrumpir o alterar ese proceso o sus agentes:

- No se toca `~/.cargo/bin/codex-bridge`; `agents-bridge` se instala aparte.
- La skill global `~/.agents/skills/codex-bridge` quedó congelada como copia
  real de la v0.4.0 (ya no enlaza al repo).
- Directorios de descriptores distintos: `agents-bridge` no lista, para ni
  envía nada a puentes `codex-bridge`.
- Hooks de prueba solo a nivel de proyecto, en una carpeta de pruebas; nunca en
  la configuración global mientras ese proyecto siga activo. La instalación
  global de la fase 4 la decide el usuario cuando sea seguro.
- La regla existente de Codex para `codex-bridge ctl` no se toca; para el nuevo
  binario hará falta una regla `agents-bridge ctl` que añade el usuario.

## 2. Hechos de partida (verificados en este equipo)

- Los "hooks de herdr" son hooks de cada harness instalados por
  `herdr integration install …`: un `SessionStart` en `~/.claude/settings.json`
  y en `~/.codex/hooks.json` que reporta la sesión a herdr.
- Codex 0.159.3 incluye los eventos `SessionStart`, `UserPromptSubmit`,
  `PreToolUse`, `PostToolUse`, `Stop` y `PermissionRequest`; Codex exige marcar
  cada hook como confiable (`[hooks.state]` en `config.toml`).
- Claude Code ofrece los mismos eventos y documenta que `Stop` puede bloquear
  el fin del turno, con `stop_hook_active` para evitar bucles.
- Sin verificar todavía: la semántica de `Stop` en Codex y si las apps de
  escritorio (Codex Desktop, pestaña Code de Claude Desktop) ejecutan los mismos
  hooks que la CLI.

## 3. Fase 0 — Comprobación (bloquea las fases 3–5)

Hook de prueba mínimo (`agents-bridge hook-probe`, o un script temporal) que
registra en un archivo privado el evento, el JSON recibido y la hora.

| Prueba | Pregunta |
|---|---|
| P1 | ¿Qué JSON recibe cada evento en Claude Code y en Codex (`session_id`, `cwd`, `tool_name`, `tool_input`, `stop_hook_active`, …)? |
| P2 | En Codex, ¿bloquear `Stop` hace que el modelo continúe? ¿Con qué formato de salida? ¿Hay límite de bloqueos? |
| P3 | En `PostToolUse` de ambos, ¿cómo se inyecta contexto (`additionalContext` u otro) y lo ve el modelo en el mismo turno? |
| P4 | ¿Codex Desktop ejecuta `~/.codex/hooks.json`? ¿La pestaña Code de Claude Desktop ejecuta los hooks de `~/.claude/settings.json`? |
| P5 | Tiempo de ejecución aceptable y qué pasa si el hook tarda o falla. |
| P6 | Windows: ejecución de un comando de hook sin shell Unix. |

Entregable: tabla de resultados en `docs/compatibility/hooks.md`. Regla: una
función que dependa de una capacidad no comprobada no se implementa como
obligatoria; se documenta como no soportada en ese harness/superficie.

## 4. Fase 1 — Renombre a agents-bridge

Cambio mecánico en su propio commit, sin cambios de comportamiento.

- Binario y comando: `agents-bridge` (`cmd/agents-bridge`). Sin alias
  `codex-bridge`: renombre directo.
- Variables: `AGENTS_BRIDGE_THEME` y `AGENTS_BRIDGE_*`; las `CODEX_BRIDGE_*`
  dejan de leerse.
- Cabecera HTTP: `X-Agents-Bridge-Request-ID`.
- Descriptores: directorio `agents-bridge/` en el runtime del usuario. Los dos
  binarios no se ven entre sí: es intencional (§1.1).
- Salida de `wait --format text`: `--- agents-bridge instance=…` (la skill
  nueva lo documenta; agentes con la skill vieja se actualizan con ella).
- Skill: `.agents/skills/agents-bridge/`; se borra `.agents/skills/codex-bridge`
  del repo. El enlace global nuevo es `~/.agents/skills/agents-bridge`.
- `codex open` se mantiene: es específico de Codex.
- Repositorio de GitHub `agents-bridge` y módulo `github.com/grrdhdz/agents-bridge`.
  La carpeta local del checkout no se renombra en esta fase (la sesión del
  orquestador y su memoria dependen de esa ruta).
- Avisar en el README de lo que el usuario debe actualizar: regla de Codex
  `prefix_rule(pattern=["agents-bridge", "ctl"], …)`, scripts y skills.

## 5. Fase 2 — Vinculación y latido

- `agents-bridge bind --instance-id ID --role ROL` vincula la sesión actual del
  harness. El agente lo ejecuta una vez al activarse; la skill y `codex open` lo
  incluyen en la instrucción de activación. `unbind` y `bind --list`.
- El vínculo se guarda en el runtime privado del usuario (como los
  descriptores), indexado por harness y `session_id`. Si el agente no conoce su
  `session_id`, `bind` lo toma del entorno que exponga el harness o se resuelve
  en el primer hook (ver P1).
- Un vínculo caduca cuando su puente ya no existe.
- Nuevo `POST /v1/heartbeat` en el plano de control: marca "trabajando" al rol
  con la hora y, opcionalmente, la herramienta en uso. Cuenta como actividad
  para `--idle-timeout`, también la del ejecutor (hoy no cuenta), pero solo
  mientras lleguen latidos: un ejecutor colgado deja de enviarlos y el puente
  vuelve a poder cerrarse.

## 6. Fase 3 — `agents-bridge hook <harness> <evento>`

Un único subcomando del binario, sin bash ni python, multiplataforma. Lee el
JSON del harness por stdin y responde en el formato de ese harness.

| Evento | Comportamiento |
|---|---|
| `SessionStart` | Si la sesión está vinculada, inyecta instancia, rol y reglas mínimas. |
| `UserPromptSubmit` | Si hay mensajes sin leer, inyecta un resumen (etiqueta, remitente, primera línea). |
| `PostToolUse` | Envía latido; hace `peek`; si hay `URGENTE` o mensajes nuevos no avisados, los inyecta como contexto (una sola vez por mensaje). |
| `PreToolUse` | Si el comando es `agents-bridge ctl send` con la bandeja sin leer y sin `--force`, lo deniega con la explicación (antes de ejecutar). |
| `Stop` | Ejecutor vinculado, puente vivo y sin `FIN`: bloquea el fin del turno con "vuelve a `ctl wait`". Respeta `stop_hook_active` y un máximo de bloqueos seguidos configurable. Si el puente ya no existe, permite parar y elimina el vínculo. El orquestador nunca se bloquea. |

Reglas comunes:

- Sesión no vinculada: salida vacía, código 0, sin tocar la red.
- Falla abierto: cualquier error o tiempo agotado (presupuesto de 2 s) deja
  continuar al agente y se registra solo en un log de diagnóstico sin cuerpos.
- Los cuerpos de mensajes inyectados se marcan como datos del otro agente, no
  como instrucciones del usuario; se recortan a un tamaño máximo.
- Nunca imprime tokens, capabilities ni rutas de control.

## 7. Fase 4 — Instalación de la integración

- `agents-bridge integration install|uninstall|status claude|codex`.
- Edita `~/.claude/settings.json` o `~/.codex/hooks.json` de forma idempotente:
  añade solo entradas propias identificadas, conserva todas las demás (incluidas
  las de herdr), hace copia de seguridad y valida el JSON resultante.
- Codex: indica al usuario que debe marcar el hook como confiable; no escribe
  `trusted_hash` por su cuenta.
- Es un cambio de configuración global: lo ejecuta el usuario. `status` muestra
  qué está instalado y si Codex lo marcó como confiable.

## 8. Fase 5 — Visibilidad, documentación y release

- `ps`, panel lateral de la TUI y `/v1/health`: "vinculado por hook", último
  latido y herramienta en uso.
- README, skill y spec. La skill deja de ser la única garantía y explica qué
  hace el hook y qué sigue dependiendo del modelo.
- Pruebas end-to-end con harnesses reales dentro de herdr (Claude Code y Codex)
  y verificación manual en las apps de escritorio según P4.
- Release `v0.5.0` con el binario `agents-bridge`.

## 9. Decisiones del usuario (2026-10-01)

1. Se renombra el repositorio de GitHub y el módulo Go a `agents-bridge`.
2. Sin alias `codex-bridge`: renombre directo.
3. `Stop` nunca bloquea al orquestador.
4. El orquestador aplica los hooks de prueba, sin interferir con el proyecto que
   usa v0.4 (§1.1).

## 10. Pruebas (TDD en todas las fases)

Renombre (nombres nuevos en binario, variables, cabecera, descriptores y skill;
ningún rastro funcional de `codex-bridge` salvo el historial); `bind` (vincular, caducar, lista); latido (estado e
inactividad); hook por evento con JSON real capturado en la fase 0 como
fixtures, en ambos formatos de salida; falla abierta y presupuesto de tiempo;
`Stop` sin bucles; instalación idempotente que preserva entradas ajenas, copia
de seguridad y desinstalación limpia; Windows en vet/build y prueba manual.
