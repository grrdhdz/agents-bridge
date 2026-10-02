# Plan: agentes locales en el mismo dispositivo

> Documento histórico anterior al renombre de 2026-10-01. Las referencias a
> `codex-bridge` describen esa versión; para el uso actual consulta la sección
> «Migración desde codex-bridge» del [README](../../../README.md).

Spec: `docs/superpowers/specs/2026-09-27-local-agents-design.md` (aprobada).
Cada paso: prueba que falla → implementación mínima → `go test -race ./...`.

1. **Cliente sin canal `events`** (`internal/bridge/client.go`)
   - Prueba: >1288 frames sin lector no bloquean; `Close` cierra las
     suscripciones con `ErrClosed`.
   - Eliminar `events`/`Events()`; `Close` cierra el `EventHub`.
   - Migrar `server_test.go` a un helper que lee del `EventHub`.
2. **Idempotencia atómica** (`PublishWithID`)
   - Prueba: N envíos concurrentes con el mismo ID → un solo envelope.
   - Mutex de publicación que cubre comprobación y registro.
3. **TUI solo con `EventHub`** (`internal/tui/model.go`)
   - Eliminar `frameMsg`/`waitForFrame`; el mensaje propio local conserva
     el estado `queued-ram`.
4. **Descriptores** (`internal/control/descriptor*.go`)
   - Pruebas: dos roles de la misma instancia coexisten; selección por rol;
     directorio con permisos incorrectos se rechaza.
   - Nombre de archivo `<instance>-<rol>.json`; UID en Unix; verificación
     de dueño y `0700`.
5. **`/v1/wait`** (`internal/control/http.go`)
   - Pruebas: mensaje del otro rol + ACK (`delivered`), ignora propios,
     timeout, cancelación sin ACK y reentrega, `WAIT_IN_PROGRESS`,
     `INSTANCE_CLOSED`.
6. **CLI `ctl` y `local`** (`cmd/codex-bridge/ctl.go`, `main.go`)
   - Pruebas: `local` publica dos descriptores y los borra al cancelar;
     `ctl send` sin ID; `ctl wait --format text` multilínea; códigos de
     salida; ambigüedad sin `--role`.
   - TUI de orquestador y `join` también publican descriptor.
7. **Skill y documentación**: `.agents/skills/codex-bridge/SKILL.md`, README.
8. **Verificación**: `go vet`, `go test -race`, builds darwin/arm64 y
   windows/amd64, prueba manual Claude ↔ Luna.
