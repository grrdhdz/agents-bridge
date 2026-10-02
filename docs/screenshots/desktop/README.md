# Capturas de escritorio

Evidencia de desarrollo, con tres puentes y datos simulados; nunca se incluye
el cliente demo en producción. Código offline y paleta de comandos añadidos en G4.

| Tema y tamaño | Inicio | Conversación | Código | Paleta |
|---|---|---|---|---|
| Claro · 1280x800 | [home](home-light-1280x800.png) | [bridge](bridge-light-1280x800.png) | [code](code-light-1280x800.png) | [palette](palette-light-1280x800.png) |
| Claro · 900x700 | [home](home-light-900x700.png) | [bridge](bridge-light-900x700.png) | [code](code-light-900x700.png) | [palette](palette-light-900x700.png) |
| Oscuro · 1280x800 | [home](home-dark-1280x800.png) | [bridge](bridge-dark-1280x800.png) | [code](code-dark-1280x800.png) | [palette](palette-dark-1280x800.png) |
| Oscuro · 900x700 | [home](home-dark-900x700.png) | [bridge](bridge-dark-900x700.png) | [code](code-dark-900x700.png) | [palette](palette-dark-900x700.png) |

Para regenerar: `cd apps/desktop && npm run screenshots` (Chromium de Playwright
en caché; `npx playwright install chromium` si falta). El script cierra Vite y
el navegador propios y comprueba teclado, foco modal y ausencia de overflow.
