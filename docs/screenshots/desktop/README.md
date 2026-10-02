# Capturas del escritorio (G3)

Fixtures de desarrollo, no datos reales. Ocho PNG con escala 1, las dimensiones
indicadas, paleta de la TUI y contraste comprobado. El demo tiene tres puentes;
la conversación incluye todas las etiquetas y estados y un mensaje humano.

| Tamaño | Tema | Inicio | Conversación |
|---|---|---|---|
| 1280 × 800 | Claro | [Inicio](home-light-1280x800.png) | [Puente](bridge-light-1280x800.png) |
| 1280 × 800 | Oscuro | [Inicio](home-dark-1280x800.png) | [Puente](bridge-dark-1280x800.png) |
| 900 × 700 | Claro | [Inicio](home-light-900x700.png) | [Puente](bridge-light-900x700.png) |
| 900 × 700 | Oscuro | [Inicio](home-dark-900x700.png) | [Puente](bridge-dark-900x700.png) |

## Regenerar

```sh
cd apps/desktop
npm ci
npx playwright install chromium  # solo si falta el navegador
npm run screenshots
```

El script arranca Vite en loopback con puerto propio, abre `?demo=1`, toma las
capturas y termina navegador y servidor en `finally`. También verifica que
Ctrl+Enter envía una intervención y que se puede volver a Inicio. El demo queda
excluido del build de producción; `npm run build` lo comprueba.
