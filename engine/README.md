# Motor agents-bridge v0.5.2

CLI, núcleo Go y TUI reutilizables por cualquier app. Compilar y verificar desde
este directorio según [AGENTS.md](../AGENTS.md); no se instala nada al compilar.
La frontera pública es la CLI y [API JSONL v1](api/README.md).

La app llama a `integration_ensure` al abrirse. `internal/integration` mantiene
la copia estable de la CLI, hooks globales, PATH de Windows y exclusiones
privadas; `internal/api` expone las tres operaciones de integración.
`agents-bridge integration ensure` devuelve el mismo resultado JSON.
Instalar/desinstalar manualmente actualiza la exclusión solo para scope user.
No se modifica la confianza de Codex ni se reemplazan entradas ajenas.

Todas las pruebas de integración aíslan HOME, LOCALAPPDATA, APPDATA,
UserConfigDir/XDG y PATH; las de Windows inyectan un registro simulado o una
clave temporal dedicada. Nunca ejecutar ensure contra el usuario real para
verificar una implementación.
