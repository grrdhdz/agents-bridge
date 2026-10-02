# Apps de agents-bridge

Las apps son clientes del motor de `engine/`. Solo usan `agents-bridge api`
(stdio JSON por líneas, pendiente de implementación) o los comandos públicos
de la CLI. Nunca importan código del motor ni leen sus descriptores o archivos
internos.

El núcleo se mantiene íntegro en Go y puede reutilizarse al 100 % desde futuras
apps nativas. El contrato compartido se publicará en `engine/api/`; las apps
pueden generar sus tipos desde ese contrato, sin duplicar lógica del motor.
La app de escritorio prevista vivirá en `apps/desktop/`.
