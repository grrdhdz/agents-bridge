# Plan: aplicación de escritorio de agents-bridge (Tauri + React)

Estado: aprobado por el usuario (Tauri + React, 2026-10-01). Ejecución autónoma
nocturna por el orquestador con Luna (Codex) en Herdr. Base: rama
`feat/agents-bridge-v0.5` una vez cerrado el plan de hooks.

## 1. Objetivo

Una app gráfica para macOS y Windows que haga todo lo que hoy hace la TUI para
el humano: ver los puentes vivos, crear y cerrar puentes locales, seguir la
conversación entre agentes, intervenir, ver el estado de cada rol y los
pendientes. La TUI y `ctl` siguen existiendo: la app es otro cliente.

Criterio de éxito: abrir la app, ver los puentes de `agents-bridge ps`, entrar
en uno, ver la conversación en vivo con Markdown y código resaltado, enviar un
mensaje como humano y que el ejecutor lo reciba con `ctl wait`; crear y cerrar
un puente local desde la app.

## 2. Monorepo: motor y apps separados (requisito del usuario)

El repositorio es un monorepo con el motor y las interfaces estrictamente
separados, para que el motor se reutilice al 100 % en futuras apps nativas:

    engine/            Módulo Go del motor: núcleo, CLI, TUI y API (go.mod propio)
      api/             Contrato de la API del motor: docs y esquema JSON versionados
    apps/desktop/      App Tauri + React (no importa nada del motor)
    docs/, .agents/    Compartidos

Reglas:

- Las apps solo hablan con el motor mediante `agents-bridge api` (stdio JSON
  por líneas) o los comandos públicos de la CLI. Nunca importan paquetes Go,
  ni leen descriptores o archivos internos del motor.
- Dentro de `engine/`, el núcleo (`protocol`, `bridge`, `control`, `bridges`,
  hooks) no importa la TUI ni la API; la TUI y la API son clientes del núcleo.
  Una prueba lo verifica con `go list -deps`.
- El contrato de la API se describe con un esquema JSON en `engine/api/` para
  que cualquier app (React hoy; SwiftUI/WinUI mañana) genere sus tipos.
- Tras el renombre, el código Go actual se mueve a `engine/` (módulo
  `github.com/grrdhdz/agents-bridge/engine`) antes de las fases de hooks, para
  no mover los archivos dos veces.

## 3. Decisiones de arquitectura

1. **El núcleo sigue en Go.** No se reimplementa en Rust la lógica de
   descriptores, ACL de Windows, health ni stop.
2. **Sidecar + API por stdio.** Nuevo subcomando `agents-bridge api`: un
   proceso de larga vida que habla JSON por líneas en stdin/stdout. La app
   Tauri lo lanza como sidecar y hace de proxy entre él y la interfaz.
   - Sin listener de red nuevo: el canal es el stdio del proceso hijo.
   - La capability y las rutas de control nunca salen del proceso Go: la
     interfaz solo ve `instance_id`, mensajes y estados.
   - Contrato versionado (`"v":1`), con peticiones `{id, op, args}` y
     respuestas/eventos `{id|sub, …}`.
3. **Operaciones de `api` v1:** `list` (lo de `ps`), `subscribe`/
   `unsubscribe` (watch de una instancia con replay), `send` (siempre
   `source=human-operator`), `stop`, `create_local` (lanza `local --headless`
   desacoplado, como la pantalla de inicio de la TUI), `health`, `export`.
4. **Interfaz:** React + TypeScript + Vite, sin framework de estilos pesado
   (CSS con variables de tema). Markdown con `react-markdown` + resaltado con
   `shiki` o `rehype-highlight` (se elige el más ligero que funcione offline).
   Paleta de comandos con `cmdk`.
5. **Temas:** claro/oscuro/automático por `prefers-color-scheme`, con tokens
   alineados con la paleta de la TUI y contraste WCAG ≥ 4.5:1 verificado por
   prueba.
6. **Sin dependencias globales nuevas:** `@tauri-apps/cli` como dependencia de
   desarrollo del proyecto (npm). Rust y Node ya están instalados.

## 4. Estructura

    engine/
      cmd/agents-bridge/api.go       subcomando api
      internal/api/                  contrato, despacho, pruebas
      api/schema.json, api/README.md contrato publicado
    apps/desktop/
      package.json, vite.config.ts, tsconfig.json
      src/                           React (pantallas, componentes, cliente api)
      src/api/types.ts               generado desde engine/api/schema.json
      src-tauri/                     Rust: sidecar, proxy, comandos/eventos
        binaries/                    agents-bridge-<target-triple>[.exe] (generados)

## 5. Fases

**G0 — Esqueleto.** Proyecto Tauri 2 + React que compila y abre una ventana en
macOS (`tauri build --debug`). Script que compila el sidecar Go para el target
actual y lo deja en `src-tauri/binaries/` con el nombre que exige Tauri.

**G1 — `api` en Go.** Contrato v1, despacho de operaciones sobre
`internal/bridges` y `internal/control` existentes, eventos de `subscribe`
reenviados en orden, cierre limpio al cerrarse stdin. Pruebas de contrato con
puentes reales en loopback (roots privados).

**G2 — Puente Rust.** Lanza el sidecar, correlaciona peticiones/respuestas,
emite los eventos a la ventana, reinicia el sidecar si muere (con aviso), y al
cerrar la app termina solo su sidecar. Pruebas en Rust con un sidecar falso.

**G3 — Pantallas principales.** Inicio (lista de puentes, crear, cerrar con
confirmación, filtro) y vista de puente: conversación tipo chat (rol local a
la derecha, otro a la izquierda, contenedores con borde del color del rol,
insignias de etiqueta, estados ✓/✓✓, origen humano/agente), panel lateral
(participantes, estado por rol, pendientes, totales), composer (etiqueta,
historial, Ctrl/Cmd+Enter envía).

**G4 — Pulido.** Markdown y código, plegado de mensajes largos, búsqueda,
paleta de comandos, avisos, diálogo de confirmación, atajos de teclado, foco
visible, accesibilidad básica (roles ARIA, navegación con teclado), temas.

**G5 — Empaquetado y documentación.** `.app`/`.dmg` de macOS sin firmar
(verificado localmente); configuración de Windows (`.msi`/NSIS) y un workflow
de GitHub Actions para compilar Windows (no se ejecuta sin push). README de la
app, sección en el README principal y nota en la skill.

## 6. Pruebas

- Go: contrato de `api` (cada operación, errores, orden de eventos,
  ausencia de secretos en toda la salida, cierre al cerrar stdin).
- Rust: correlación de peticiones, reinicio del sidecar, cierre limpio.
- Frontend: Vitest + Testing Library por componente, con un cliente `api`
  falso; prueba de contraste de los tokens de tema.
- Integración: un puente local real, la app (o su frontend con el sidecar real
  en modo desarrollo) muestra los mensajes enviados con `ctl` y lo que envía la
  app llega a `ctl wait`.
- Evidencia visual: capturas del frontend en ambos temas para la revisión.

## 7. Autoauditoría (riesgos y decisiones)

| Riesgo | Mitigación / decisión |
|---|---|
| "Tauri y React Native" no se combinan | Confirmado por el usuario: Tauri + React (web en WebView). React Native queda fuera. |
| Mezclar motor e interfaz | Monorepo con  y  separados; las apps solo usan el contrato; prueba de dependencias del núcleo. |
| Duplicar lógica del núcleo en Rust | Prohibido por diseño: sidecar Go + stdio. Rust solo hace de proxy. |
| Exponer la capability a la WebView | El contrato nunca la incluye; prueba de que ninguna salida de `api` contiene capability, tokens ni `control_url`. |
| Puertos de red nuevos | Ninguno: stdio. El plano de control sigue en loopback dentro de cada puente. |
| Compilar Windows desde macOS | Tauri no soporta oficialmente compilar el instalador de Windows en macOS. Se compila y prueba el sidecar Go para Windows, se deja `cargo check --target x86_64-pc-windows-msvc` del lado Rust si es posible, y el instalador queda para CI o un equipo Windows. Se documenta como pendiente verificado. |
| Firma y notarización | Requieren cuentas del usuario (Apple Developer ID, certificado Windows). Fuera de esta entrega; builds sin firmar marcados como tales. |
| Tamaño y arranque | Sidecar Go ~25 MB + WebView del sistema. Se mide y se documenta. |
| Alcance de una noche | G0–G3 son el mínimo para revisión; G4–G5 se completan en lo posible y lo que falte queda listado. |
| Procesos huérfanos | La app solo termina su sidecar; los puentes creados con `create_local` siguen la regla de inactividad y se listan al salir, como en la TUI. |
| Convivencia con codex-bridge v0.4 | El sidecar es `agents-bridge` con su propio directorio de descriptores: no ve ni toca puentes v0.4. |
| Dependencias npm | Solo paquetes conocidos y mantenidos; `npm audit` en el informe; lockfile en el repo. |

## 8. Fuera de alcance

Firma/notarización, actualizaciones automáticas, instalación de hooks desde la
app (se mostrará solo el estado si `integration status` existe), Tailscale
desde la app (se puede observar un puente de Tailscale, no crearlo), móvil.
