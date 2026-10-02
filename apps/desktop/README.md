# agents-bridge: app de escritorio

Cliente Tauri 2 + React + TypeScript + Vite. G2 mantiene un único proceso
`agents-bridge api` por ventana. Los comandos Rust reflejan las nueve operaciones
públicas; no leen archivos del motor ni contienen reglas de negocio.

El proxy correlaciona IDs, aplica un timeout (5 s para hello, 30 s para las otras
operaciones) y emite `agents-bridge-event` / `agents-bridge-status` únicamente a
la ventana propietaria. Al caer el proceso cancela las peticiones pendientes,
notifica a la UI y reinicia con espera creciente (250 ms–5 s). Solo restaura las
suscripciones, con IDs estables para la ventana; nunca repite envíos, creaciones
ni cierres. El replay se deduplica en la interfaz por `message_id`. Un unsubscribe durante
la caída elimina la intención antes del reinicio, evitando restaurar vistas
abandonadas.

Al destruir la ventana o salir de la app, el proxy cierra stdin y, si el sidecar
no termina, finaliza solo ese proceso. Nunca envía Stop por su cuenta: los
puentes siguen vivos. Las pruebas Rust compilan un sidecar falso desde
`src-tauri/tests/fixtures/` y comprueban correlación fuera de orden, eventos,
timeouts, caída, restauración y cierre. No se necesitan instalaciones globales.

## Separación

El runtime de la app solo usa la CLI pública y el contrato JSONL v1. No importa
Go, no lee descriptores, vínculos ni archivos internos del motor. Rust hace de
proxy de las operaciones; las reglas del puente permanecen en Go. La WebView no recibe
permisos para ejecutar comandos arbitrarios.

Las únicas referencias de compilación al motor son:

- `scripts/sidecar.mjs`: compila `engine/cmd/agents-bridge` como ejecutable.
- `scripts/types.mjs`: genera TypeScript desde `engine/api/schema.json`, el
  contrato público versionado. `src/api/types.ts` se incluye en Git.

## Desarrollo

Requisitos: Node compatible con Vite 8 (20.19+ o 22.12+), npm, Rust y las
herramientas nativas de Tauri; Go 1.27+ para compilar el sidecar. No se instala
nada globalmente.

```sh
cd apps/desktop
npm ci
npm run generate:types
npm run sidecar
npm run tauri dev
```

`tauri dev` y `tauri build` compilan automáticamente el sidecar. El script usa
`TAURI_ENV_TARGET_TRIPLE`, después `CARGO_BUILD_TARGET`, o el host de `rustc`.
Admite arm64/amd64 en macOS, Windows y Linux. Escribe únicamente en
`src-tauri/binaries/agents-bridge-<target-triple>[.exe]`, que Git ignora.

## Verificación

```sh
npm run check:types           # falla si cambia el contrato sin regenerar
npm test                     # scripts, hello real + EOF y componentes React
npm run build                # tipos, TypeScript y Vite
cargo fmt --manifest-path src-tauri/Cargo.toml -- --check
cargo test --manifest-path src-tauri/Cargo.toml
cargo check --manifest-path src-tauri/Cargo.toml
npm run tauri build -- --debug
npm run smoke:native         # paquete macOS → React → Rust → Go
npm audit
```

Build macOS de desarrollo, sin firma de distribución:
`src-tauri/target/debug/bundle/macos/agents-bridge.app`.
No hace falta un servidor Vite para abrir ese paquete; sus recursos están
incluidos y no usa fuentes ni estilos de red.

Para comprobar el target Windows desde macOS:

```sh
CARGO_BUILD_TARGET=x86_64-pc-windows-msvc npm run sidecar
cargo check --manifest-path src-tauri/Cargo.toml --target x86_64-pc-windows-msvc
```

El sidecar Go puede compilarse desde macOS. Las herramientas de Windows para
Rust/WebView y el empaquetado deben verificarse en Windows; no se instala un
SDK ni se fuerza una cadena cruzada en esta fase.

## Resultado histórico de G0 (2026-10-01)

| Comprobación | Resultado |
|---|---|
| Tipos generados y `check:types` | OK; se observó fallar antes de generar el archivo |
| Scripts Node | 3 pruebas OK: targets, separación y sidecar real + EOF |
| React / Vitest + Testing Library | 2 pruebas OK: versión recibida y error de conexión |
| `npm run build` | OK, Vite 8.3.2; JS 220.88 kB (69.14 kB gzip) |
| `cargo test --lib` | OK; prueba de respuesta v1/ID/errores, observada fallar antes de implementar el decoder |
| `cargo fmt -- --check` / `cargo check` macOS | OK |
| `npm run tauri build -- --debug` | OK, `.app` de 54.42 MiB |
| `npm run smoke:native` | OK: `Motor conectado: v0.4.0 · API v1`; la app empaquetada ejecutó el handshake solicitado desde React |
| Sidecar Go Windows amd64 | OK |
| `cargo check --target x86_64-pc-windows-msvc` | Limitado por el entorno, detalle debajo |
| `npm audit` | 0 vulnerabilidades |

El target Rust de Windows ya estaba instalado. La comprobación alcanzó el
build script de la app y falló en `tauri-winres 0.3.6`, `src/lib.rs:543`, con:

```text
called `Result::unwrap()` on an `Err` value: NotAttempted("llvm-rc")
```

Falta el compilador de recursos de Windows `llvm-rc`. No se instaló un SDK ni
se modificó el entorno para forzar la compilación cruzada.

Computer Use rechazó la inspección de la ventana porque `agents-bridge` no
estaba autorizada. No se obtuvo una captura ni una revisión visual nativa. La
prueba ejecutable del paquete confirma la carga de la WebView y el recorrido
del `invoke` real; el render de la versión se comprueba en DOM con Testing
Library. La prueba cierra únicamente su grupo de procesos y usa un runtime
temporal; no deja la app ni su sidecar vivos.

## G3: pantallas y demo de desarrollo

Inicio muestra los puentes con filtro, estado por rol, mensajes e inactividad;
refresca cada 2 s. Crear usa `create_local` y cerrar requiere un diálogo modal
con Cancelar como acción inicial. La conversación usa replay observador,
alineación por el rol local, bordes por rol, etiquetas, origen y estados
`·` / `✓` / `✓✓` / `✗`. El panel muestra hooks, herramientas, latido y métricas.

`Pendientes de leer por <rol>` viene del campo opcional `health.unread`, mediante
peek sin consumo. Si no está disponible se muestra `—`. `Enviados sin entregar`
cuenta mensajes locales enviados/aceptados sin confirmación de entrega en la
vista; no se presenta como bandeja del otro agente.

El composer conserva hasta 20 envíos de su vista en memoria, recuperables desde
Historial; Ctrl/Cmd+Enter envía. Los fallos conservan el borrador y muestran el
mensaje rechazado. Una suscripción inicial fallida se reintenta; las ya activas
las restaura Rust. Los eventos que llegan antes de responder subscribe se
almacenan temporalmente hasta conocer el ID. Al volver a Inicio se cancela la
suscripción. Los temas Claro/Oscuro/Automático responden al tema del sistema,
y sus tokens pasan contraste WCAG de texto ≥4.5:1 y bordes ≥3:1.

```sh
npm run typecheck
npm test
npm run build
# Solo desarrollo: abrir http://localhost:1420/?demo=1 tras npm run dev
npx playwright install chromium  # si no está en caché
npm run screenshots
```

El demo se carga únicamente dentro de `import.meta.env.DEV`, por import dinámico.
Producción elimina su cliente y sus datos. El build ejecuta un guard que busca
marcadores de los fixtures y falla si aparecen; una prueba verifica que el guard
rechaza datos simulados. No existe un modo demo activable por URL en producción.

Las [capturas](../../docs/screenshots/desktop/README.md) muestran tres
puentes y una conversación con las siete etiquetas, los cuatro estados de
entrega, intervención humana y panel completo. Playwright crea y cierra su
servidor Vite y navegador propios; el script también comprueba Ctrl+Enter,
ausencia de errores del frontend y de desbordamiento horizontal.

### Verificación G2/G3 (2026-10-02)

- Rust: 3 pruebas con proceso falso, `cargo fmt`, `cargo test` y `cargo check` OK.
- Node: 4 pruebas (sidecar real/EOF, nombres, separación, exclusión de demo).
- Vitest/Testing Library: 10 pruebas, incluidos pantallas, composer, mensajes,
  sidebar, replay y contraste.
- `npm run typecheck`, `npm run build`, screenshots y `npm audit` OK.
- Motor: gofmt vacío, vet nativo/Windows, race y builds darwin/Windows OK.
  Se observó fallar la prueba de unread antes de implementar el campo.
- `tauri build --debug` macOS y smoke nativo OK; ruta `.app` indicada arriba.
- Sin capturas nativas: esta fase usa las capturas Playwright solicitadas.
  La limitación de Windows por `llvm-rc` sigue siendo la documentada en G0.

### Pulido de escritorio (G4)

Markdown y código se renderizan offline, sin HTML ejecutable ni imágenes remotas.
Los mensajes de más de 30 líneas se pliegan. Cmd/Ctrl+F busca por mensaje con
resaltado y navegación; Cmd/Ctrl+K abre la paleta. `?` muestra la ayuda fuera
de campos de texto. Exportar Markdown/JSONL usa el diálogo nativo de guardar
y la API pública. Los temas incluyen contraste comprobado para el código;
la interfaz respeta movimiento reducido y los diálogos mantienen el foco.

## G4: herramientas de conversación

- Markdown seguro (`react-markdown`) y código offline (`rehype-highlight`,
  lenguajes comunes). HTML crudo se descarta; imágenes no se cargan y enlaces
  se muestran como texto, sin navegación ni recursos remotos.
- Mensajes largos: las primeras 30 líneas, con expansión accesible. Una búsqueda
  revela automáticamente el contenido con coincidencias.
- Cmd/Ctrl+F: búsqueda literal, sin distinguir mayúsculas. Las coincidencias se
  agrupan por mensaje; Enter/Shift+Enter o flechas avanzan/retroceden.
- Cmd/Ctrl+K: paleta accesible con inicio, crear/cerrar puente, copiar ID,
  exportar Markdown/JSONL, búsqueda y temas. Las acciones de un puente se
  deshabilitan en Inicio. `?` abre la ayuda fuera de campos de texto.
- Exportar elige la ruta en el diálogo de guardar de Tauri y llama a `export`;
  cancelar no escribe. El permiso de WebView se limita a `dialog:allow-save`.
- Avisos de conexión, reconexión, rechazo, copia y cierre. Un rechazo conserva
  el borrador. Los diálogos atrapan el foco, Esc cancela y el foco se devuelve
  al control anterior. `prefers-reduced-motion` desactiva movimiento.
- Latidos relativos legibles y ocho caracteres alfanuméricos para IDs cortos;
  un clic copia siempre el ID completo.

Las pruebas cubren cada pieza y el recorrido con un cliente falso. Playwright
comprueba además teclado y el foco del diálogo HTML. Los tokens de código
pasan contraste ≥4.5:1 y los bordes ≥3:1 en ambos temas. Las 16 capturas incluyen
inicio, conversación, código y paleta en dos temas y dos tamaños.
