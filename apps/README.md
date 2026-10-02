# Apps de agents-bridge

Las apps son clientes del motor de `engine/`. Solo usan `agents-bridge api`
(stdio JSON por líneas, contrato v1) o los comandos públicos
de la CLI. Nunca importan código del motor ni leen sus descriptores o archivos
internos.

El núcleo se mantiene íntegro en Go y puede reutilizarse al 100 % desde futuras
apps nativas. El contrato compartido se publica en `engine/api/`; las apps
pueden generar sus tipos desde ese contrato, sin duplicar lógica del motor.
La app de escritorio prevista vivirá en `apps/desktop/`.

## Escritorio

[`desktop/`](desktop/README.md) contiene el esqueleto Tauri 2 + React y sus
comandos de desarrollo, generación de tipos y compilación del sidecar.
