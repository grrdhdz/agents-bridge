# Compatibilidad de hooks

## Entorno y alcance

Ensayos del 2026-10-01 (México; 2026-10-02 UTC), macOS, Claude Code
**2.1.287** (`claude-sonnet-5-5`) y Codex CLI **0.159.3** (`gpt-6.1-sol`).
CLI no interactivas, herramientas y puente loopback reales. Apps de escritorio
y ejecución de hooks en Windows pendientes; vet/build Windows sí pasan.

El binario E2E se compiló desde `58c4d93eb2850e87e80f7a151c8a44783439943f`
en `agents-bridge-probe/e2e/bin/agents-bridge`; sin instalarlo. La revisión final
incluye el golden de hooks y una corrección del cambio de rol visible con prueba
real de puente local. No se publica release ni se crean tags.

Logs completos: `/Users/grrdhdz/Documents/DESARROLLO/agents-bridge-probe/e2e/<harness>/`.
Las referencias de líneas siguientes corresponden a esos logs. Las selecciones
sin rutas personales se versionan en `evidence/<harness>-hooks.json`.
TAREA/canarios son datos artificiales de esta prueba; el motor no persiste cuerpos.

## Resumen de fase 0

Originales: `agents-bridge-probe/RESULTADOS.md` y `evidence/<harness>-<prueba>/`.

| Prueba | Claude | Codex |
|---|---|---|
| P1 | Cinco eventos en `claude -p`; `tool_input.command` string, `tool_response` objeto. | Cinco eventos en `codex exec`; `tool_response` string y `transcript_path=null` con `--ephemeral`. |
| P2, block-stop=1 | Ejecuta `echo CANARIO-STOP`; siguiente Stop con `stop_hook_active=true`. | Igual. |
| P2, block-stop=5 | Cinco continuaciones, seis Stop y termina. | Igual; no establece el máximo absoluto del harness. |
| P3 | Contexto de PostToolUse incluye CANARIO-URGENTE en el mismo turno. | Igual. |
| P5, sleep 12 / timeout 10 | Hook cancelado; el agente continúa. | Continúa; corte inferido por falta de final del hook. El stream no proporciona su duración. |
| P5, JSON truncado con exit 0 | No impide terminar. | Igual. |

Campos comunes según evento: `session_id`, `cwd`, `hook_event_name`, `tool_name`,
`tool_input`, `tool_response`, `stop_hook_active`, `transcript_path`. Fixtures
reales sanitizados: `engine/internal/hooks/testdata/`. Medianas del script
original P1: Claude 16–23 ms, Codex 14–21 ms; replay de tres muestras/evento
sin modelo ni red, no garantía de presupuesto en otros equipos.

Formatos comprobados en ambos:

```json
{"decision":"block","reason":"ejecuta ctl wait…"}
```

```json
{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"…"}}
```

En fase 0 fue necesaria confianza de proyecto persistida; un override `-c
projects…` por sí solo no bastó. Se retiró la entrada global y restauraron sus
hashes. La validación E2E usa confianza persistida en CODEX_HOME temporal.

## Método E2E

- `integration install <harness> --scope project --project DIR` crea los cinco
  handlers. `installed.json` y `installed-config.json` conservan el resultado.
- Solo para medir, `observe.py` envuelve cada comando instalado, invoca el
  binario real con el JSON y devuelve su stdout sin modificarlo. `hooks.jsonl`
  registra entrada, salida y tiempo del proceso Go. Timeout del handler 5 s;
  presupuesto interno 2 s. Instrumentación externa exclusiva de esta prueba.
- Puente `local --headless --idle-timeout 20s --ready-file ready.json`, runtime
  privado por harness mediante TMPDIR. El controlador solo usa su instancia.
- Prompt mínimo: «usa agents-bridge como ejecutor con --instance-id ID: ejecuta
  agents-bridge ctl wait … y haz lo que pida el mensaje», más ruta del binario
  y restricción de usar exclusivamente este puente y el shell.
- TAREA: ocho herramientas separadas `echo TRABAJO-i; sleep 4`. Durante TRABAJO-2
  llega un URGENTE con un canario ausente del prompt. Se pide imprimirlo antes de
  leer con wait, intentar un envío sin consumirlo (E7), consumirlo y continuar.
- Tras RESULTADO, la TAREA pide intentar terminar sin esperar voluntariamente
  para probar Stop. El controlador envía FIN solo al observar el wait posterior
  al bloqueo. La espera inicial, la de URGENTE y la provocada por Stop se
  distinguen por tiempos y comandos.
- Sondeos ps/peek cada 2 s, sin actividad ni presencia de espera del orquestador.
  Solo se consume RESULTADO cuando llega; no hay latidos artificiales ni mensajes
  periódicos durante el trabajo prolongado.
- Claude: fuentes project, Bash permitido, MCP vacío, sin persistencia.
  Codex: CODEX_HOME temporal con copia privada de autenticación (retirada al
  terminar), configuración mínima y proyecto trusted, `--ephemeral`,
  `--ignore-rules`, `--dangerously-bypass-hook-trust` y
  `--dangerously-bypass-approvals-and-sandbox` para loopback. Bypass de confianza
  solo por invocación; no se persisten hashes. El segundo ensayo usa
  `model_reasoning_effort="low"` para reducir intervalos entre herramientas.
- Sin HERDR_* ni CLAUDECODE heredados. Sin hooks globales, instalaciones,
  modificaciones de skills congeladas ni procesos de otros proyectos.
  Límite 240 s por ejecución; limpieza limitada a grupos propios.

## Reproducción

Desde la raíz, usando una carpeta de pruebas nueva:

```sh
PROBE=/ruta/aislada/agents-bridge-probe/e2e
mkdir -p "$PROBE/bin"
cp docs/compatibility/e2e/*.py "$PROBE/"
(cd engine && go build -o "$PROBE/bin/agents-bridge" ./cmd/agents-bridge)
python3 "$PROBE/run_e2e.py" claude
python3 "$PROBE/run_e2e.py" codex
```

Requiere Python 3 y harnesses autenticados. El runner rehúsa sobrescribir evidencia
existente. Instala/retira únicamente scope project; CODEX_HOME temporal elimina
confianza y copia de autenticación al finalizar. `global-before.json`,
`global-after.json` y `cleanup.json` comprueban la limpieza. Mantener observe.py
junto al runner. Los permisos del proyecto no deben impedir las escrituras de
los vínculos en el runtime privado ni la conexión loopback.

Fuera de la prueba, aprobar hooks desde Codex al reiniciar («Review Hooks» /
«Trust All and Continue») o `/hooks` y `t`; el instalador nunca concede confianza.
Status compara hashes de solo lectura conforme a
[discovery.rs](https://github.com/openai/codex/blob/main/codex-rs/hooks/src/engine/discovery.rs)
y [fingerprint.rs](https://github.com/openai/codex/blob/main/codex-rs/config/src/fingerprint.rs).
Se contrastó además el hash calculado con la entrada existente de herdr aprobada,
sin modificarla. Flags de sesión/políticas pueden alterar el estado efectivo.

## Resultados E1–E7

`C:N` y `X:N` indican la línea N de `claude/hooks.jsonl` y
`codex/hooks.jsonl` respectivamente. Ambas ejecuciones finales: exit 0, sin
alcanzar el límite de 240 s. Duración de sesión: Claude 65.74 s, Codex 107.43 s.

| Prueba | Claude | Codex | Evidencia |
|---|---|---|---|
| E1, primer ctl vincula | OK | OK | C:3 / X:7, PreToolUse del primer wait; `orchestrator.jsonl` evento bindings con instancia y session_id reales. ps muestra hook_bound=true. |
| E2, TAREA → trabajo → RESULTADO | OK, ocho herramientas | OK, ocho herramientas | Post de TRABAJO-1…8; C:33 / X:31 envían TRABAJO-OK y canario. `summary.json.result` contiene el mensaje recibido por el orquestador. |
| E3, Stop fuerza nueva espera sin FIN | OK | OK | C:34 / X:32 devuelven decision=block; C:35 / X:33 ejecutan wait antes del envío de FIN. Evento stop_forced_wait. |
| E4, URGENTE por PostToolUse en el mismo turno | OK, un aviso | OK, un aviso | C:8 / X:12 inyectan el canario; C:9 / X:13 lo imprimen antes del siguiente ctl wait. El código no existía en prompt/TAREA. |
| E5, FIN permite terminar | OK | OK | C:36 / X:34, Post del wait que consume FIN; C:37 / X:35 Stop sin salida, stop_hook_active=true. Peek final fin_received=true, exit 0. |
| E6, latidos sostienen trabajo >20 s | OK, tramo 51.535 s | OK, tramo 72.014 s | ps durante el trabajo: mayor idle 6 s / 9 s, herramienta Bash y timestamps de latidos. Sin presencia permanente del orquestador. |
| E7, denegación con bandeja sin leer | OK, respetada | OK, respetada | C:17 / X:15: permissionDecision=deny. No hay PostToolUse con ese tool_use_id ni entrega de RESULTADO/ENSAYO-GUARDA. Después ambos consumen URGENTE antes del envío válido. |

En Codex el stream no expone el comando bloqueado como command_execution;
la salida del hook, ausencia de Post con ese tool_use_id, lectura posterior y
bandeja del orquestador establecen que la denegación se respetó. El resultado
final menciona el ensayo denegado: esa mención no es una entrega del mensaje de
ensayo. Claude hizo primero dos intentos con flags inexistentes; produjeron
error de uso. La denegación documentada corresponde al comando válido con
`--body-file -`, reconocido por el hook.

### Duración y límites

| Medida | Claude | Codex |
|---|---:|---:|
| Mediana del proceso Go del hook | 20.6 ms | 19.5 ms |
| Máximo observado | 39.3 ms | 24.0 ms |
| Tiempo de trabajo entre primera Pre y última Post | 51.535 s | 72.014 s |

Estos tiempos del wrapper incluyen arranque del binario, no el intérprete,
modelo ni cola del harness. Todos quedan dentro del presupuesto de 2 s.

El primer Codex, conservado en `e2e/codex-gap-20s/`, ejecutó E1/E4/E7 y parte
del trabajo, pero dejó de emitir herramientas/latidos más de 20 s. Último latido
`2026-10-01T23:04:54.469645-06:00`; ps aún vivo con idle=19 a
`1790917514.296014`, ausente a `1790917516.435893`. El puente cerró por
inactividad conforme al diseño. No establece un defecto de heartbeat ni una
garantía de mantener vivo al modelo mientras razona sin herramientas. El
segundo ensayo redujo esos intervalos con esfuerzo low y completó E1–E7.

El primer arranque de Claude (`e2e/claude-setup-1/`) se cerró por una suposición
incorrecta del runner sobre ps: JSONL devuelve un objeto con instances, no una
fila por instancia. Se corrigió el runner antes del ensayo completo. En la
limpieza del primer Codex, killpg devolvió EPERM para un proceso ya terminado;
se auditó que no quedaban comandos del proyecto, se retiraron credenciales y
runtime, y se completó cleanup.json. El runner actual comprueba poll antes de
matar su grupo y siempre retira las credenciales aunque haya error de limpieza.

## Verificación y cierre

Antes de cada uno de los tres commits: gofmt sin pendientes, vet nativo y
Windows, `go test -race -count=1 ./...`, builds darwin/arm64 y windows/amd64.
TDD observado: instalador y CLI sin API (errores de compilación), binding ausente
en health, campos ausentes en ps/TUI, golden inexistente cambio de rol que
dejaba el vínculo antiguo visible y os.Executable con un nombre personalizado. Todos se corrigieron y pasan.

`e2e/analyze.py` comprueba automáticamente E1–E7 y cleanup sobre la evidencia;
se ejecutó con ambos resultados en true. Puede reproducirse sin lanzar modelos:

```sh
python3 docs/compatibility/e2e/analyze.py /ruta/agents-bridge-probe/e2e
```

Los tres archivos globales tienen los mismos SHA-256 antes/después en ambos
harnesses (`global-before.json` / `global-after.json`), incluido config.toml.
No se escribió ni retiró una entrada de confianza en el HOME real. La confianza
temporal fue exactamente `[projects."<proyecto-codex>"] trust_level="trusted"`
en CODEX_HOME temporal, retirado completamente después. No se escribió
trusted_hash ni hooks.state en ninguna configuración global. Las entradas
instaladas en proyectos de prueba fueron retiradas; runtime y copias privadas
de autenticación eliminados, todos los procesos propios terminados.

Los builds locales conservan el identificador de versión previo hasta la tarea
de release. Estos resultados describen el commit indicado, no un release v0.5.0
publicado. La revisión visual en apps, ejecución Windows y aprobación persistida
de hooks sin el bypass permanecen pendientes y no se presentan como verificadas.
