# Especificación: coordinación entre agentes (v0.4.0)

Estado: aprobada (2026-09-29)

Origen: retroalimentación de un orquestador que usó codex-bridge con un
ejecutor Codex en un proyecto real. Base: `v0.3.4`.

## 1. Problemas a resolver

1. Mensajes que se cruzan: el ejecutor envió `RESULTADO` sin leer una
   `RESPUESTA` que invalidaba su diseño.
2. No hay forma de interrumpir: un "detente" solo llega cuando el ejecutor
   vuelve a `wait`.
3. Silencios opacos: esperas de más de una hora sin señal de progreso.
4. Canal de escalamiento ambiguo: decisiones de diseño pedidas en la app del
   agente en vez de por el puente.
5. La evidencia de la conversación solo queda en la transcripción de un
   agente.
6. Errores de uso que la herramienta puede prevenir o aclarar.

## 2. No objetivos

- Sin cambios de protocolo TCP. `reply_to` queda aplazado (cambia el
  envelope; la guarda de §3.1 ya evita el cruce).
- Sin log automático de cuerpos: el invariante "todo en RAM" se mantiene.
  La exportación (§3.5) solo escribe cuando alguien la pide.

## 3. Cambios

### 3.1 Guarda de bandeja en `send`

- `POST /v1/send` acepta `require_inbox_empty` (bool). `ctl send` lo envía
  en `true` por defecto; `--force` lo envía en `false`.
- "Sin leer" = mensajes del otro rol con `event_seq` posterior al cursor de
  consumo de `wait` de ese endpoint. La comprobación se hace en el servidor
  bajo el mismo bloqueo que el cursor (sin carreras).
- Si hay mensajes sin leer: HTTP 409, código `INBOX_NOT_EMPTY`, salida 5, con
  `unread` y la etiqueta del más reciente en el mensaje. No se publica nada.
- Los envíos con `source=human-operator` (TUI) nunca se bloquean.
- Excepción: los mensajes cuya etiqueta (primera línea) sea `URGENTE` o `FIN`
  saltan siempre la guarda, para ambos roles, aunque `require_inbox_empty` sea
  `true`: son interrupciones o cierres y no deben quedar bloqueados por
  mensajes sin leer. `TAREA`, `PREGUNTA`, `RESPUESTA`, `RESULTADO` y
  `PROGRESO` siguen sujetos a la guarda.

### 3.2 `ctl peek`

- `GET /v1/peek` y `codex-bridge ctl peek --instance-id ID --role ROL
  [--format jsonl|text]`: devuelve cuántos mensajes del otro rol hay sin leer,
  si alguno es `URGENTE` y la etiqueta y `message_id` del más reciente. No
  confirma, no mueve el cursor, no cuenta como presencia.
- Texto: `--- codex-bridge instance=ID unread=N urgent=sí|no latest=ETIQUETA`.

### 3.3 Etiquetas nuevas

- `URGENTE`: interrumpe; el ejecutor la atiende en su siguiente `peek`.
  Insignia de color de peligro en la TUI.
- `PROGRESO`: nota breve de avance del ejecutor. Insignia atenuada.
- `ctrl+t` en el composer incluye ambas.

### 3.4 Estado por rol

- Cada endpoint registra: `wait` en curso, hora del último `wait` y hora del
  último mensaje enviado por su rol. En `local` ambos endpoints comparten un
  registro, así que cada uno conoce el estado de los dos roles; en
  `tailscale-host` y `tailscale-join` solo el propio.
- Estado derivado: `esperando` (hay un `wait` en curso), `trabajando` (sin
  `wait`, con actividad en los últimos 15 min), `callado` (sin `wait` ni
  actividad en 15 min), `—` (desconocido).
- `/v1/health` expone `roles: {orchestrator|executor: {state, last_message_at,
  last_wait_at}}`. `ps` muestra columnas `ORQ` y `EJEC` con el estado y la
  antigüedad del último mensaje; el panel lateral de la TUI lo muestra por
  participante.

### 3.5 `ctl export`

- `codex-bridge ctl export --instance-id ID --role ROL --output FILE
  [--format md|jsonl]`: vuelca la conversación retenida en RAM a un archivo
  nuevo (creación exclusiva, solo el propietario). No confirma mensajes.
- Markdown: cabecera con instancia y hora de exportación, y un bloque por
  mensaje (rol, origen, hora, estado, etiqueta, cuerpo). JSONL: los envelopes.
- Si el journal ya expulsó mensajes antiguos, el archivo lo indica al
  principio.

### 3.6 Skill

- `ctl read` y `ctl peek` leen sin confirmar; `wait` nunca pierde un mensaje
  aunque tenga un timeout corto (solo confirma tras entregarlo).
- Ejecutor: `ctl peek` entre pasos largos (cada test, cada iteración); si hay
  `URGENTE`, parar y leerlo con `wait`. `PROGRESO` cada ~10 min de trabajo.
- Nunca enviar `RESULTADO` con la bandeja sin leer; si `send` responde
  `INBOX_NOT_EMPTY`, leer primero.
- Escalamiento: decisiones de diseño y alcance por el puente (`PREGUNTA`);
  permisos de sandbox y aprobaciones en la app del propio agente.
- Orquestador: puede exportar la conversación con `ctl export` como evidencia.

## 4. Pruebas

Guarda (bloquea, `--force`, humano nunca bloqueado, sin carreras con `wait`
concurrente), `peek` (no consume, urgente, texto), etiquetas en TUI (golden),
estado por rol en `local`, host y join, columnas de `ps`, panel lateral,
`export` (Markdown y JSONL, archivo existente, permisos, journal expulsado),
regresión completa. Verificación habitual de AGENTS.md.
