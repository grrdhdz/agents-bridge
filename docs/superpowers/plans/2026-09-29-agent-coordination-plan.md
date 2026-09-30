# Plan: coordinación entre agentes (v0.4.0)

Spec: `docs/superpowers/specs/2026-09-29-agent-coordination-design.md` (aprobada).
Cada paso: prueba que falla → implementación mínima → `go test -race ./...`.

1. **Registro de estado por rol** (`internal/control/roles.go`)
   - Pruebas con reloj inyectable: `esperando`, `trabajando` (< 15 min),
     `callado` (≥ 15 min), `—`; dos `wait` no terminan antes de tiempo.
2. **Guarda de bandeja** (`internal/control/http.go`)
   - Cursor de consumo bajo `cursorMu` (no `waitMu`, que un `wait` mantiene
     hasta 30 min); comprobación + publicación bajo el mismo bloqueo.
   - Pruebas: bloquea con mensajes sin leer, campo ausente/`false` publica,
     `human-operator` nunca se bloquea, serialización con el cursor, carrera
     con `wait` concurrente bajo `-race`.
3. **`GET /v1/peek`**: no consume, no cuenta como presencia ni como `wait`.
4. **`/v1/health` con `roles`** y `Endpoint.RoleSnapshots` (reproduce el
   journal para contar mensajes enviados por cualquier vía).
5. **CLI**: `ctl send` (guarda por defecto, `--force`), `ctl peek`,
   `ctl export` (creación exclusiva vía `control.ReadyFile`), salida 5 para
   `INBOX_NOT_EMPTY`; `ps` con columnas `ORQ`/`EJEC`; `local` comparte el
   registro entre endpoints.
6. **TUI**: etiquetas `URGENTE`/`PROGRESO` (`splitLabel`, insignias, `ctrl+t`),
   estado por rol en el panel lateral; golden nuevos.
7. **Skill y README** (§3.6), `appVersion` a `v0.4.0`.
8. **Verificación**: `gofmt -l .`, `go vet` (darwin y windows),
   `go test -race -count=3 ./...`, builds darwin/arm64 y windows/amd64.
