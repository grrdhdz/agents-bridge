# agents-bridge: app de escritorio

Versión **0.5.3**, identificador **dev.grrdhdz.agents-bridge**.
Cliente Tauri 2 + React + TypeScript + Vite. G2 mantiene un único proceso
`agents-bridge api` por ventana. Los comandos Rust reflejan las operaciones
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
herramientas nativas de Tauri; Go 1.27+ para compilar el sidecar. Las dependencias se instalan localmente. **Abrir la app** mantiene automáticamente
los hooks de usuario y una CLI estable; compilar y ejecutar tests no lo hace.

```sh
cd apps/desktop
npm ci
npm run generate:types
npm run sidecar
npm run tauri dev           # ventana nativa + Vite + sidecar
# Frontend solamente: npm run dev (sin Tauri, usa ?demo=1)
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
npm run smoke:native         # paquete debug macOS → React → Rust → Go
npm run smoke:native -- --release # verificar el paquete release
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
  cancelar no escribe. El exportador del motor requiere un archivo nuevo y no
  sobrescribe destinos existentes. El permiso de WebView se limita a `dialog:allow-save`.
- Avisos de conexión, reconexión, rechazo, copia y cierre. Un rechazo conserva
  el borrador. Los diálogos atrapan el foco, Esc cancela y el foco se devuelve
  al control anterior. `prefers-reduced-motion` desactiva movimiento.
- Latidos relativos legibles y ocho caracteres alfanuméricos para IDs cortos;
  un clic copia siempre el ID completo.

Las pruebas cubren cada pieza y el recorrido con un cliente falso. Playwright
comprueba además teclado y el foco del diálogo HTML. Los tokens de código
pasan contraste ≥4.5:1 y los bordes ≥3:1 en ambos temas. Las 24 capturas actuales
incluyen inicio, conversación, código, paleta, panel de hooks y panel plegado en dos temas y dos
tamaños.

## G5: empaquetado

```sh
cd apps/desktop
npm ci
npm run tauri build          # release; compila motor, frontend y Rust
# macOS: src-tauri/target/release/bundle/macos/agents-bridge.app
# macOS: src-tauri/target/release/bundle/dmg/agents-bridge_0.5.3_aarch64.dmg
# Windows (equipo Windows): .../bundle/msi/*.msi y .../bundle/nsis/*.exe
npm run tauri icon -- src-tauri/icons/app.svg  # regenerar iconos desde SVG
```

La base configura `.app`/`.dmg`; `tauri.windows.conf.json` se mezcla automáticamente
para generar MSI y NSIS (por usuario, selector español/inglés). Iconos de flechas
azul/verde distinguen los roles y se incluyen en PNG, ICNS e ICO. El paquete
se llama agents-bridge y los tres manifiestos coinciden en 0.5.3.

No hay identidad de firma configurada, certificado Developer ID/Windows ni
notarización. Un binario Mach-O puede tener la firma ad hoc del enlazador; eso
no es una firma de distribución. Gatekeeper o SmartScreen pueden exigir la
aprobación del usuario. No se ofrece actualización automática.

El instalador Windows no se genera desde macOS. La comprobación cruzada Rust
ya documentada falla por `NotAttempted("llvm-rc")`; hace falta la cadena nativa
Windows para validar el instalador y los hooks (P6). El sidecar Go para Windows
sí se verifica localmente.

[Workflow](../../.github/workflows/desktop.yml): macos-latest/windows-latest,
Node 22, Go 1.27 y Rust stable. Ejecuta tipos, tests, fmt/test/check y build,
conservando paquetes como artefactos; no publica releases. YAML analizado con
PyYAML y estructura/expresiones revisadas; actionlint no está disponible. No se
ha ejecutado en GitHub porque no se hizo push. Las rutas de acciones son desde
la raíz y los comandos npm/Cargo desde apps/desktop.

El demo es exclusivo de Vite dev. Producción ejecuta un guard de ausencia de
fixtures y los paquetes incluyen únicamente el cliente real. La paleta,
Markdown y resaltado no requieren red. El bundle JS actual mide 618.02 kB
(192.59 kB gzip); Vite avisa del umbral de 500 kB, sin impedir el build.
Los tamaños del release comprobado quedan registrados en el informe de revisión.

### Release verificado en macOS (2026-10-02)

| Artefacto | Tamaño |
|---|---:|
| `src-tauri/target/release/bundle/macos/agents-bridge.app` | 34 887 836 bytes · 33.27 MiB |
| `src-tauri/target/release/bundle/dmg/agents-bridge_0.5.2_aarch64.dmg` | 15 387 130 bytes · 14.67 MiB |

SHA-256 del DMG: `b40c4adb4f0a8c1bcc82ab122d2ecd353126ec9be1eeb3582e2241a254c98a1c`.
Bundle `dev.grrdhdz.agents-bridge`, versión 0.5.2, sin `_CodeSignature` de bundle.
Smoke del release: `Motor conectado: v0.5.2 · API v1`; procesos propios cerrados.
Verificación del rediseño: 6 pruebas Node, 36 Vitest, 4 Rust, typecheck/build,
fmt/test/check Cargo, release Tauri y audit (0 vulnerabilidades), todos OK.
Motor: gofmt, vet nativo/Windows, race y builds Darwin/Windows OK; con pruebas nuevas de mantenimiento automático. La intermitencia histórica de reconexión sigue documentada
en el informe de revisión.

## Presentación de escritorio

Inicio liso y conversación con fondo crema suave en claro y gris azulado
profundo en oscuro (`--chatBg`), sin cuadrícula ni imágenes externas. Controles
flotantes y notas pastel: azul para orquestador, verde para ejecutor y amarillo
para intervención humana. La galería abre con
«Nuevo puente local»; las miniaturas reutilizan los últimos mensajes recibidos
en la vista, sin nuevas llamadas al motor, o muestran notas grises si no hay datos.
El panel lateral se puede plegar sin perder mensajes ni borrador. La barra de
herramientas tiene nombres accesibles y tooltips; todos los atajos siguen activos.

Tipografía del sistema y SVG de Lucide empaquetados, sin CDN. Contraste de texto,
chips y código ≥4.5:1 sobre cada relleno y el fondo de chat en ambos temas;
bordes/foco ≥3:1.
Hover discreto y transiciones desactivadas con `prefers-reduced-motion`.

## Hooks automáticos (0.5.2)

Al arrancar, la app pide `integration_ensure` una vez, sin confirmación previa.
Rust solo reenvía la operación; Go mantiene CLI, hooks y elecciones de usuario.
No se reejecuta al refrescar puentes ni al reconectar el sidecar.
El indicador **Hooks ✓ / Hooks: revisar** abre un diálogo accesible con estado
por harness, CLI actual/PATH y **Mantener hooks instalados**. Desactivar guarda
la exclusión y quita solo nuestros hooks; actualizar no revoca esa elección.
Los fallos no bloquean el chat y se muestran sin reintento automático en bucle.

Los hooks son de usuario, para todos los proyectos. La ruta estable evita
cambiar el comando al actualizar. Codex requiere aceptar la confianza y
confiar en el proyecto; no editamos `trusted_hash` ni `[hooks.state]`.
Windows actualiza el PATH de usuario; abre una terminal nueva. En macOS los
hooks usan rutas absolutas y no se modifican perfiles de shell.

`smoke:native -- --release` crea HOME, LOCALAPPDATA, APPDATA/UserConfigDir,
XDG y PATH temporales antes de lanzar la app. Espera hello e integration_ensure,
comprueba la CLI v0.5.3 y los cinco hooks de ambos harnesses, y cierra únicamente
su grupo de procesos. La app nunca lee archivos internos del motor: el smoke
comprueba exclusivamente el ejecutable y configuración pública de los harnesses.
No usar un HOME real al hacer smoke o pruebas manuales de instalación.
