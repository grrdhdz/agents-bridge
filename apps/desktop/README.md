# agents-bridge: app de escritorio

Cliente Tauri 2 + React + TypeScript + Vite. G2 mantiene un único proceso
`agents-bridge api` por ventana. Los comandos Rust reflejan las nueve operaciones
públicas; no leen archivos del motor ni contienen reglas de negocio.

El proxy correlaciona IDs, aplica un timeout (5 s para hello, 30 s para las otras
operaciones) y emite `agents-bridge-event` / `agents-bridge-status` únicamente a
la ventana propietaria. Al caer el proceso cancela las peticiones pendientes,
notifica a la UI y reinicia con espera creciente (250 ms–5 s). Solo restaura las
suscripciones, con IDs estables para la ventana; nunca repite envíos, creaciones
ni cierres. El replay se deduplica en la interfaz por `message_id`.

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
cargo test --manifest-path src-tauri/Cargo.toml --lib
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

## Resultado de G0 (2026-10-01)

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
