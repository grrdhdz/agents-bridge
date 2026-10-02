# AGENTS.md — agents-bridge

Instrucciones compartidas para todos los agentes (Codex, Claude Code y otros).
Este archivo es la única fuente de instrucciones del proyecto. `CLAUDE.md` solo
importa este archivo; no agregues instrucciones allí.

## Propósito

`agents-bridge` es un canal de texto efímero (solo RAM) entre dos agentes:

- Objetivo original: un orquestador en macOS y un ejecutor en Windows, conectados
  por Tailscale.
- Objetivo ampliado: dos agentes en el mismo dispositivo (p. ej. Claude Code
  como orquestador y un agente de la app Codex como ejecutor), sin Tailscale.

## Idioma y estilo

- Documentación, mensajes de TUI y specs en español; identificadores y código en
  inglés, siguiendo el estilo del código existente.
- Comentarios con la misma densidad que el código vecino: explican el porqué.

## Comandos

```sh
gofmt -l .        # debe quedar vacío; corrige con gofmt -w
go build ./...
go vet ./...
go test ./...
go test -race ./...
go build -o agents-bridge ./cmd/agents-bridge
GOOS=windows GOARCH=amd64 go build -o agents-bridge.exe ./cmd/agents-bridge
```

Requiere Go 1.27+. Antes de declarar un cambio terminado: `gofmt -l .` vacío,
`go vet`, `go test -race ./...` y ambos builds (darwin/arm64 y windows/amd64).

## Invariantes del proyecto

- Todo estado de chat vive en RAM: sin SQLite, archivos de historial ni logs de
  cuerpos. El descriptor de control es metadata efímera, no historial.
- Cada invocación crea una instancia aislada (`instance_id`, puerto y tokens
  propios). Instancias concurrentes no comparten nada.
- El protocolo TCP v1 (`internal/protocol`) no cambia de forma incompatible sin
  una spec aprobada que suba la versión.
- Nunca imprimir ni registrar pairing tokens, reconnect tokens ni capabilities
  fuera de los canales ya definidos.
- El plano de control local (`internal/control`) solo escucha en loopback.

## Mapa del código

- `cmd/agents-bridge` — CLI: orquestador con TUI (por defecto), `join`,
  `local` (dos agentes en el mismo equipo) y `ctl` (control no gráfico).
- `internal/protocol` — frames, envelopes y validación.
- `internal/bridge` — `Server` (Mac), `Client`, `EventHub` (fan-out + journal).
- `internal/control` — endpoint HTTP loopback y descriptores (`ctl`, v0.2.0).
- `internal/tui` — TUI Bubble Tea.
- `internal/tailscale`, `internal/clipboard` — integración de plataforma.
- `docs/superpowers/specs/` — especificaciones aprobadas.

## Skills

Las skills compartidas viven en `~/.agents/skills/<nombre>/SKILL.md` (y, si
existe, `.agents/skills/` en este repo, que tiene prioridad). Cualquier agente
debe leer el `SKILL.md` correspondiente y seguirlo cuando la tarea encaje con su
descripción, aunque su herramienta no las cargue de forma nativa. En particular:

- `brainstorming` — obligatoria antes de diseñar funcionalidades o cambios de
  arquitectura. Las specs aprobadas se guardan en
  `docs/superpowers/specs/YYYY-MM-DD-<tema>-design.md`.
- `context7-mcp` — cuando el trabajo dependa de documentación actual de una
  librería (Bubble Tea v2, etc.).
- `find-skills` — para descubrir skills nuevas.

Skills nuevas del proyecto se crean en `.agents/skills/`, no en directorios
específicos de una herramienta (`.claude/`, `.codex/`). `.claude/skills` es un
symlink a `.agents/skills` para que Claude Code las cargue.

- `agents-bridge` (en este repo) — cómo dos agentes se comunican por el puente
  local como orquestador o ejecutor.

## Procesos y búsquedas

- Nunca busques en todo el disco (`find /`, `bfs /`, `grep -r /`, etc.).
  Limita las búsquedas al repositorio o a rutas concretas, por ejemplo el caché
  de módulos de Go: `$(go env GOMODCACHE)`. Para ubicar el código de una
  dependencia usa `go list -m -f '{{.Dir}}' <módulo>`.
- No dejes procesos en segundo plano al terminar una tarea: cierra lo que
  lances (puentes con `agents-bridge stop --instance-id`, esperas `ctl wait`,
  servidores de prueba).

## Flujo de trabajo

- Cambios pequeños y bien especificados: implementar directamente con pruebas.
- Funcionalidades nuevas o cambios de interfaz: seguir `brainstorming` (diseño
  aprobado → spec → plan) antes de implementar; TDD en la implementación.
- No hacer commits, push ni releases sin autorización explícita del usuario.
