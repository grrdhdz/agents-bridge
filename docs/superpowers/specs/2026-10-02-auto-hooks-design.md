# Hooks automáticos al instalar o actualizar la app (v0.5.1)

Estado: aprobado por el usuario (2026-10-02). Decisiones: totalmente
automático, sin aviso previo; hooks a nivel de usuario (globales), válidos para
todos los proyectos presentes y futuros sin reinstalar.

## 1. Objetivo

Al abrir la app de escritorio por primera vez, o tras actualizarla, los hooks de
Claude Code y Codex quedan instalados y funcionando para el usuario, sin pasos
manuales. Proyectos nuevos no requieren configuración.

## 2. Flujo

En cada arranque, la app llama a `integration_ensure` del motor. El motor:

1. **Instala la CLI en una ruta estable** (copia atómica de su propio ejecutable):
   - macOS/Linux: `~/.local/bin/agents-bridge`.
   - Windows: `%LOCALAPPDATA%\agents-bridge\bin\agents-bridge.exe`, y añade ese
     directorio al `PATH` del usuario (`HKCU\Environment`, con
     `WM_SETTINGCHANGE`) si falta.
   - Solo reemplaza el binario si su hash difiere. Si el destino existe y no es
     un agents-bridge (no responde `--version` con el prefijo `agents-bridge`),
     no lo sobrescribe y lo reporta.
2. **Instala los hooks de usuario** de `claude` y `codex` con
   `integration.Apply("install", scope=user)` apuntando a la ruta estable. La
   ruta estable mantiene igual el comando del hook entre versiones, así Codex no
   pide confiar de nuevo tras cada actualización.
3. Solo actúa si la versión cambió desde el último `ensure` correcto o si el
   estado no coincide (falta un hook, apunta a otra ruta o falta la CLI). En otro
   caso solo devuelve el estado (arranques rápidos, sin escrituras).

Respeta una exclusión explícita: si el usuario desactiva "Mantener hooks
instalados" en la app o ejecuta `agents-bridge integration uninstall <harness>`,
se guarda `opted_out` para ese harness y `ensure` no lo reinstala, ni siquiera
al actualizar. `integration install` o reactivar el interruptor borra la
exclusión.

Estado del motor (metadata, no historial):
`<UserConfigDir>/agents-bridge/integration-state.json` con
`{ "version": "v0.5.1", "cli_path": "...", "opted_out": {"claude": false, "codex": false} }`,
escrito con permisos privados.

## 3. Interfaz

API stdio v1, solo añadidos compatibles (el esquema y los tipos generados se
actualizan):

- `integration_status` → por harness: `installed`, `opted_out`, `path` del
  archivo de configuración, `trust_note` (Codex), más `cli_path`,
  `cli_on_path`, `cli_current`.
- `integration_ensure` → mismo resultado más `changed`.
- `integration_set {harness, enabled}` → instala/desinstala y fija `opted_out`.

CLI: `agents-bridge integration ensure` (mismo comportamiento, salida JSON) para
pruebas y soporte.

App: indicador en la barra superior ("Hooks ✓" / "Hooks: revisar") que abre un
panel con el estado por harness, el interruptor "Mantener hooks instalados" por
harness, la ruta de la CLI y avisos (Codex: aceptar hooks y confiar en el
proyecto; Windows: abrir terminal nueva para el PATH; CLI fuera del PATH). Un
aviso no bloqueante si `ensure` falla; la app sigue funcionando.

## 4. Seguridad y convivencia

- Solo se tocan entradas propias (`OwnedCommand`); las demás, incluidas las de
  codex-bridge v0.4, quedan intactas. Copia de seguridad antes de modificar,
  como ya hace `Apply`.
- Los hooks no actúan en sesiones no vinculadas a un puente agents-bridge
  (`ErrNotBound` → salida vacía), así que no afectan a otros proyectos ni a
  sesiones de codex-bridge v0.4.
- Ningún token ni capability en el estado, la salida de la API ni los logs.
- Fallos (JSON de configuración inválido, permisos, destino ajeno) se reportan
  por harness y no se reintentan en bucle; no modifican el archivo.

## 5. Pruebas

- Go (con `HOME`, `LOCALAPPDATA`, `UserConfigDir` temporales, nunca la
  configuración real): primera instalación; idempotencia (segundo `ensure` sin
  escrituras); actualización de versión reemplaza la CLI y conserva el comando
  del hook; reparación de hook faltante o con otra ruta; `opted_out` respetado
  tras actualizar; destino ajeno no sobrescrito; entradas de terceros intactas;
  configuración inválida intacta; ausencia de secretos en la salida.
- Windows: `go vet` y pruebas de la lógica de PATH tras una interfaz
  (registro simulado); prueba real en CI `windows-latest` con `LOCALAPPDATA`
  temporal.
- App: Vitest del indicador, panel e interruptor; llamada a `ensure` al iniciar
  con cliente falso; contraste WCAG.
- Smoke nativo: arranque del paquete release con `HOME` temporal deja la CLI y
  los hooks de ambos harness instalados.

## 6. Fuera de alcance

Firma, auto-actualización de la app, aceptar la confianza de hooks en Codex por
el usuario (Codex lo exige de forma interactiva), modificar perfiles de shell en
macOS.
