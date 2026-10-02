# Plan: visibilidad y control humano de los puentes

> Documento histórico anterior al renombre de 2026-10-01. Las referencias a
> `codex-bridge` describen esa versión; para el uso actual consulta la sección
> «Migración desde codex-bridge» del [README](../../../README.md).

Spec: `docs/superpowers/specs/2026-09-27-bridge-visibility-design.md` (aprobada).
Ejecución delegada a un subagente por `codex-bridge`; el orquestador revisa y
verifica cada fase antes de enviar la siguiente. TDD en todas.

## Fase 1 — ver y cerrar puentes

1. Descriptor: campos `mode` y `last_activity_at`; `Start*` reciben el modo.
2. `peer_connected` correcto por modo (`Server.WorkerConnected()` para host;
   ambos clientes en `local`).
3. Seguimiento de actividad (mensajes + presencia del orquestador) y
   `--idle-timeout` en `local` (defecto 30m) y host (defecto 0).
4. `POST /v1/stop` con restricción por rol/modo; callback de cierre inyectado.
5. CLI `ps [--format table|jsonl]` y `stop --instance-id`.

## Fase 2 — TUI observadora

1. `source` opcional en `/v1/send` (`agent-control` por defecto,
   `human-operator`); cabecera de `wait` con `source=`.
2. Límite de 8 `watch` por endpoint.
3. Interfaz de transporte en `internal/tui`; adaptadores `Client` y plano de
   control; TUI actual sin cambios de comportamiento.
4. `codex-bridge tui --instance-id`; `/stop`; reconexión del watch.
5. `local`: TUI propia si stdout es TTY, `--headless`, `--ready-file`.
6. Skill: arranque visible, `tui`, `stop`, prioridad de `human-operator`.

## Fase 3 — Tailscale sin TUI

1. `join --headless` con descriptor `tailscale-join` y reconexión.
2. Host Mac `--headless` + `--ready-file` obligatorio con `join_command`.
3. README y verificación final completa.
