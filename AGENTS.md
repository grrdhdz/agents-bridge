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
cd engine
gofmt -l .        # debe quedar vacío; corrige con gofmt -w
go build ./...
go vet ./...
GOOS=windows go vet ./...
go test ./...
go test -race -count=1 ./...
go build -o agents-bridge ./cmd/agents-bridge
GOOS=windows GOARCH=amd64 go build -o agents-bridge.exe ./cmd/agents-bridge
```

Requiere Go 1.27+. Antes de declarar un cambio terminado: `gofmt -l .` vacío,
`go vet ./...`, `GOOS=windows go vet ./...`, `go test -race -count=1 ./...` y
ambos builds (darwin/arm64 y windows/amd64), desde `engine/`.

## App de escritorio

Desde `apps/desktop/` (Node 20.19+/22.12+, Rust, Go 1.27, herramientas nativas):

```sh
npm ci
npm run sidecar             # binario propio por target, sin instalar en PATH
npm run dev                 # frontend; ?demo=1 solo en desarrollo
npm run tauri dev           # ventana nativa con Vite y sidecar
npm run generate:types      # desde el contrato público engine/api/schema.json
npm run check:types
npm run typecheck
npm test                    # Node + Vitest/Testing Library
npm run build               # incluye guard: demo excluido de producción
cargo fmt --manifest-path src-tauri/Cargo.toml -- --check
cargo test --manifest-path src-tauri/Cargo.toml
cargo check --manifest-path src-tauri/Cargo.toml
npm run tauri build         # macOS .app/.dmg; Windows MSI/NSIS en Windows
npm run smoke:native -- --release
npm run screenshots         # 24 PNG; cierra Vite y Playwright propios
npm audit
```

CLI Tauri de desarrollo local. Abrir la app v0.5.2 mantiene automáticamente
hooks globales y CLI estable: todos los tests/smoke deben lanzar procesos con
HOME, LOCALAPPDATA, APPDATA/UserConfigDir/XDG y PATH temporales. Nunca usar
`integration install|ensure` contra el usuario real para verificar cambios. Antes de cerrar cambios en la app,
verificar tipos/tests/build, fmt/test/check Cargo y build de Tauri. Capturas
solo de desarrollo en `docs/screenshots/desktop/`. Los tokens, incluidos código
y búsqueda, pasan contraste WCAG; los diálogos y atajos tienen pruebas de teclado.
Solo se permite leer el contrato público para generar tipos y compilar el motor
como sidecar; el runtime nunca accede a archivos internos del motor. La prueba
de separación vive en `apps/desktop/scripts/separation.test.mjs`.


## Invariantes del proyecto

- Todo estado de chat vive en RAM: sin SQLite, archivos de historial ni logs de
  cuerpos. El descriptor de control y los vínculos de sesiones son metadata de
  coordinación, no historial.
- Cada invocación crea una instancia aislada (`instance_id`, puerto y tokens
  propios). Instancias concurrentes no comparten nada.
- El protocolo TCP v1 (`engine/internal/protocol`) no cambia de forma
  incompatible sin una spec aprobada que suba la versión.
- Nunca imprimir ni registrar pairing tokens, reconnect tokens ni capabilities
  fuera de los canales ya definidos.
- El plano de control local (`engine/internal/control`) solo escucha en loopback.

## Mapa del código (monorepo)

- `engine/` — módulo Go `github.com/grrdhdz/agents-bridge/engine`.
- `engine/cmd/agents-bridge` — CLI: host, `join`, `local`, `ctl`, `ps`, `stop`, `rename`,
  `tui`, `bind`, `unbind`, `hook`, `integration`, `api` y `codex open`.
- `engine/internal/protocol` — frames, envelopes y validación.
- `engine/internal/bridge` — `Server`, `Client`, `EventHub` (fan-out + journal).
- `engine/internal/control` — endpoint HTTP loopback y descriptores.
- `engine/internal/bridges` — registro y listado de puentes.
- `engine/internal/hooks` — vínculos, cursores, hooks del harness y coordinación,
  núcleo independiente de la TUI.
- `engine/internal/integration` — hooks por usuario/proyecto, CLI estable, exclusiones y PATH Windows.
- `engine/internal/api` — cliente stdio JSONL v1 del núcleo; nunca importa la TUI.
- `engine/internal/bridgeexport` — exportador compartido de CLI y API.
- `engine/internal/tui` — TUI Bubble Tea, cliente del núcleo.
- `engine/internal/tailscale`, `engine/internal/clipboard` — integración de
  plataforma.
- `engine/api/` — esquema JSON v1, documentación y validador estructural del contrato.
- `engine/architecture_test.go` — verifica la separación de dependencias con
  `go list -deps`.
- `apps/` — interfaces independientes; la app Tauri 2 + React vive en `apps/desktop/`.
- `apps/desktop/src/api/` — tipos públicos generados y cliente Tauri tipado.
- `apps/desktop/src/screens/`, `components/` — inicio, chat, panel, Markdown,
  búsqueda, paleta, composer, diálogos accesibles y avisos; estado en React.
- `apps/desktop/src/demo/` — fixtures exclusivos de dev; guard de producción.
- `apps/desktop/src-tauri/src/` — proxy stdio, timeouts y recuperación; sin negocio.
- `apps/desktop/src-tauri/tauri*.conf.json`, `icons/` — bundles por plataforma,
  metadatos 0.5.4 e iconos propios. Sin firma de distribución/notarización.
- `.github/workflows/desktop.yml` — builds macOS/Windows y artefactos; sin release.
- `docs/REVIEW-2026-10-02.md` — entrega, cómo probar y pendientes de publicación.
- `docs/`, `.agents/skills/` — documentación y skills compartidas en la raíz.

## Separación del motor y las apps

- Las apps solo usan `agents-bridge api` o los comandos públicos de la CLI.
  Nunca importan código Go del motor ni leen sus descriptores o archivos internos.
- El núcleo (`protocol`, `bridge`, `control`, `bridges` y futuros paquetes de
  hooks) no depende de la TUI ni de la API, ni siquiera de forma transitiva.
  La TUI y la API son clientes del núcleo.
- Todo código Go y su módulo viven en `engine/`; las apps pueden generar tipos
  desde el contrato versionado de `engine/api/`, sin duplicar lógica del motor.

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
