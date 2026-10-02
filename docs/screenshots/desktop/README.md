# Capturas de escritorio

Fondos lisos: inicio limpio y conversación crema en claro / gris azulado
profundo en oscuro. Barras flotantes, galería con miniaturas sin puntos, notas
pastel por rol y origen humano, panel plegable e iconos SVG offline. Evidencia de desarrollo con tres puentes simulados; el cliente demo
nunca se incluye en producción.

| Tema y tamaño | Inicio | Conversación | Código | Paleta | Panel plegado | Hooks |
|---|---|---|---|---|---|---|
| Claro · 1280x800 | [home](home-light-1280x800.png) | [bridge](bridge-light-1280x800.png) | [code](code-light-1280x800.png) | [palette](palette-light-1280x800.png) | [collapsed](collapsed-light-1280x800.png) | [hooks](hooks-light-1280x800.png) |
| Claro · 900x700 | [home](home-light-900x700.png) | [bridge](bridge-light-900x700.png) | [code](code-light-900x700.png) | [palette](palette-light-900x700.png) | [collapsed](collapsed-light-900x700.png) | [hooks](hooks-light-900x700.png) |
| Oscuro · 1280x800 | [home](home-dark-1280x800.png) | [bridge](bridge-dark-1280x800.png) | [code](code-dark-1280x800.png) | [palette](palette-dark-1280x800.png) | [collapsed](collapsed-dark-1280x800.png) | [hooks](hooks-dark-1280x800.png) |
| Oscuro · 900x700 | [home](home-dark-900x700.png) | [bridge](bridge-dark-900x700.png) | [code](code-dark-900x700.png) | [palette](palette-dark-900x700.png) | [collapsed](collapsed-dark-900x700.png) | [hooks](hooks-dark-900x700.png) |

**24 PNG**, en dos temas y dos tamaños. La conversación comienza por TAREA;
la captura de código muestra las últimas notas y FIN. La captura plegada
conserva la conversación y el composer, ampliando el espacio de conversación.
El panel de hooks muestra la instalación global simulada y confianza pendiente de Codex.

Para regenerar: `cd apps/desktop && npm run screenshots` (Chromium de Playwright
en caché; `npx playwright install chromium` si falta). El script cierra Vite y
el navegador propios y comprueba teclado, foco modal, historial visible,
plegado sin perder mensajes, fondos sin cuadrícula y ausencia de overflow horizontal.
