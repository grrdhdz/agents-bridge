# Plan: rediseño de la TUI (v0.3.0)

Spec: `docs/superpowers/specs/2026-09-27-tui-redesign-design.md` (aprobada).
Estado: **fases 1–3 completadas** (versión `v0.3.0`).
Ejecución delegada a un subagente por `codex-bridge`; el orquestador revisa,
verifica y prueba en la terminal real cada fase antes de enviar la siguiente.
TDD en todas; ningún cambio de protocolo, plano de control ni `ctl`.

## Fase 1 — Base (completada)

1. `theme/`: paletas claro/oscuro/auto, colores semánticos, `NO_COLOR`,
   `--theme` y `CODEX_BRIDGE_THEME`.
2. `keys/`: mapa de atajos central y ayuda generada.
3. Shell (`app.go`), capacidades (§4) y pantalla de puente con barra de estado,
   conversación con tarjetas (sin Markdown), composer con etiqueta e
   historial, barra de atajos y ayuda.
4. Migrar los cuatro modos (host, join, local, observadora) conservando las
   pruebas de seguridad y de ACK/observadora; golden a 60/80/120 columnas.

## Fase 2 — Contenido (completada)

1. glamour + resaltado con caché; plegado; selección, copia y búsqueda.
2. `StatusProvider` y panel lateral; avisos; paleta y diálogos.

## Fase 3 — Inicio (completada)

1. Pantalla de lista de puentes, acciones y navegación inicio ↔ puente.
2. Documentación, versión v0.3.0.

Notas de la fase 3: la lista de puentes vive en `internal/bridges` (compartida
con `ps` y `stop`); el shell `App` (`internal/tui/root.go`) alterna entre la
pantalla de inicio (`home.go`) y la vista de un puente; `n` lanza
`codex-bridge local --headless --ready-file` desacoplado
(`cmd/codex-bridge/home.go`).
