# Especificación: rediseño de la TUI

> Documento histórico anterior al renombre de 2026-10-01. Las referencias a
> `codex-bridge` describen esa versión; para el uso actual consulta la sección
> «Migración desde codex-bridge» del [README](../../../README.md).

Estado: aprobada (2026-09-27)

Fecha: 2026-09-27

Base: `v0.2.1` (`main`). Versión objetivo: `v0.3.0`.

## 1. Propósito

Llevar la interfaz de terminal de `codex-bridge` al nivel de un harness maduro
(referencia: opencode): legible, navegable con teclado, coherente en todos los
modos y útil para supervisar varios puentes.

Criterio de éxito: el usuario abre `codex-bridge tui`, ve sus puentes, entra a
uno, sigue la conversación con Markdown y código resaltado, sabe en todo
momento quién está conectado y qué falta por leer, interviene con etiquetas,
y descubre cualquier acción desde la paleta o la ayuda, sin memorizar nada.

## 2. No objetivos

- No cambia el protocolo TCP, el plano de control, `ctl` ni los transportes
  (`Transport` y sus dos implementaciones siguen igual; §7 solo añade una
  interfaz opcional).
- No hay vista simultánea de varios puentes (pestañas o paneles múltiples).
- No hay archivo de configuración persistente: el tema se elige con flag o
  variable de entorno. Todo el estado de chat sigue en RAM.
- No hay soporte de ratón más allá de rueda y trackpad para desplazar.

## 3. Arquitectura

`internal/tui` pasa de un modelo único a una app de componentes Bubble Tea:

    internal/tui/
      app.go            shell: pantalla activa, tamaño, tema, overlays
      theme/            paletas claro/oscuro, estilos, estilo de glamour
      keys/             mapa de atajos central (bubbles/key) + ayuda
      screens/home      lista de puentes (§5)
      screens/bridge    vista de un puente (§6)
      components/       statusbar, conversation, message, sidebar,
                        composer, palette, dialog, toast, help
      transport.go      sin cambios (+ StatusProvider opcional, §7)
      control_transport.go  sin cambios de comportamiento

Reglas:

- Cada componente es un modelo pequeño con su propio `Update`/`View` y pruebas.
- Solo el shell conoce el tamaño de la terminal y lo reparte.
- Los atajos se declaran una vez en `keys/`; la ayuda y la barra inferior se
  generan desde ahí. La barra inferior ocupa siempre una sola línea: muestra
  los atajos que quepan por prioridad y termina en `? más`.
- Las diferencias entre modos se expresan como **capacidades** (§4), nunca con
  ramas de `Update` por modo.
- Todas las TUIs usan pantalla alternativa y restauran la terminal al salir.

## 4. Modos y capacidades

| Capacidad | Host Mac | Join | `local` (embebida) | `tui` (observadora) |
|---|:-:|:-:|:-:|:-:|
| Enviar como | humano (orq.) | humano (ejec.) | humano (orq.) | humano (rol del endpoint) |
| Confirmar mensajes (ACK) | sí | sí | no | no |
| Cerrar el puente al salir | sí | sí (solo join) | sí | no |
| `stop` desde la paleta | sí | sí | sí | sí (con confirmación) |
| Emparejar (`/pair`, F5) | sí | no | no | no |
| Volver a la pantalla de inicio | no | no | no | sí |

El shell recibe un struct `Capabilities` construido por cada modo en
`cmd/codex-bridge`; los componentes consultan capacidades, no modos.

## 5. Pantalla de inicio (`codex-bridge tui` sin `--instance-id`)

- Lista de puentes vivos del usuario (misma fuente que `ps`: descriptores +
  `/v1/health`), refrescada cada 2 s. Columnas: instancia (corta), modo,
  roles, conectado, inactivo, mensajes, inicio.
- Acciones: `enter` entra (observadora), `s` cierra con diálogo de
  confirmación, `n` crea un puente `local --headless` nuevo con el
  `--idle-timeout` por defecto y entra en él (el proceso queda en segundo
  plano: sigue vivo al salir de la TUI, aparece en `ps` y se cierra con `stop`
  o por inactividad; la TUI lo avisa al salir), `r` refresca, `/` filtra, `q`
  sale.
- Estado vacío con instrucciones (`n` para crear uno, cómo lanzar `local`).
- Desde la vista de un puente abierto así, `esc` vuelve a la lista.
- `codex-bridge tui --instance-id ID` sigue entrando directo al puente; en ese
  modo directo no hay inicio al que volver (la capacidad `ReturnHome` solo se
  activa al entrar desde la lista) y el cierre del puente termina la TUI.
- Cada fila muestra además el proyecto (base del cwd del descriptor) y `enter`
  o el doble clic entran; `s` cierra por el mismo camino que `codex-bridge stop`.
- Si el puente abierto desde inicio se cierra (stop externo, inactividad), se
  vuelve a la lista con un aviso.

## 6. Vista de un puente

### 6.1 Diseño

    ┌ barra de estado ─────────────────────────────────────────────┐
    │ conversación                                  │ panel lateral │
    │                                               │               │
    ├ composer ────────────────────────────────────────────────────┤
    └ atajos ──────────────────────────────────────────────────────┘

- **Adaptable:** con ≥ 110 columnas se muestra el panel lateral; con menos se
  oculta y se alterna con `ctrl+b`; con < 60 columnas las tarjetas se
  compactan (sin márgenes ni barra de color).
- **Foco:** `tab` alterna entre composer y conversación; el foco se ve en el
  borde activo.

### 6.2 Barra de estado

Nombre del proyecto (base de `cwd` del descriptor si existe), instancia corta,
modo, reloj de inactividad (`inactivo 3m / 30m`), e indicador del otro rol
(● conectado / ○ desconectado / ◌ reconectando). Los cambios de conexión
generan además un aviso (§6.7).

### 6.3 Conversación

- **Tarjeta por mensaje:** barra de color por rol; cabecera con icono de rol,
  nombre del rol (siempre Orquestador/Ejecutor, nunca "Tú": en la
  observadora el rol del endpoint también lo usa un agente), origen (agente /
  humano, que es como se distinguen tus mensajes), hora y estado (`·` en cola, `✓` aceptado, `✓✓` entregado, `✗`
  rechazado).
- **Disposición de chat:** las tarjetas del rol local de la TUI (el rol con
  el que escribe: orquestador en host y `local`, el del endpoint en la
  observadora, ejecutor en join) se alinean a la derecha y las del otro rol a
  la izquierda, con un ancho máximo del 80 % de la conversación (100 % en modo
  compacto). Los mensajes humanos van del lado de su rol y se distinguen por
  el origen.
- **Etiqueta:** si la primera línea es `TAREA`, `PREGUNTA`, `RESPUESTA`,
  `RESULTADO` o `FIN`, se muestra como insignia de color y se quita del cuerpo.
- **Cuerpo en Markdown** con glamour (`charm.land/glamour/v2`) y resaltado de
  código, con el estilo del tema. El render se cachea por
  `(message_id, ancho, tema)`.
- **Mensajes largos** (> 30 líneas renderizadas) se pliegan con
  "▸ N líneas más"; `enter` sobre el mensaje seleccionado los despliega.
- **Selección (foco en conversación):** `↑/↓` o `k/j` mueven entre mensajes;
  `y` copia el cuerpo al portapapeles; `enter` plegar/desplegar; `g/G` inicio
  y final.
- **Seguimiento:** si el usuario está al final, los mensajes nuevos desplazan
  la vista; si no, aparece "↓ N mensajes nuevos" y `G` o `end` baja.
- **Búsqueda:** `ctrl+f` abre un campo; resalta coincidencias y `n/N` salta
  entre ellas.

### 6.4 Panel lateral

- **Puente:** instancia completa, modo, tiempo activo, inactividad.
- **Participantes:** Orquestador y Ejecutor con presencia (●/○) y última
  actividad de cada uno (hora de su último mensaje).
- **Pendientes:** mensajes del rol local aún no entregados, con su estado y
  antigüedad; se resaltan si superan 5 minutos sin `delivered`.
- **Totales:** mensajes por rol.

### 6.5 Composer

- Multilínea, `ctrl+s`/`ctrl+enter` envía, `enter` nueva línea (como hoy).
- **Selector de etiqueta** en el borde (`ctrl+t` rota: ninguna → TAREA →
  PREGUNTA → RESPUESTA → FIN); la etiqueta se antepone como primera línea.
- **Historial:** con el composer vacío, `↑/↓` recorre lo enviado en esta
  sesión (RAM).
- El marcador de posición indica el rol y el origen con que se enviará.
- Los comandos `/stop`, `/quit`, `/status`, `/pair` siguen funcionando.

### 6.6 Paleta de comandos y ayuda

- `ctrl+p` abre una paleta con filtro difuso de las acciones disponibles según
  capacidades: cerrar puente, copiar instance_id, copiar comando de unión
  (host), volver a inicio, cambiar tema, alternar panel, buscar, ayuda.
- `?` (foco en conversación) o `ctrl+/` muestra la ayuda generada desde el
  mapa de atajos, agrupada por contexto.

### 6.7 Diálogos y avisos

- **Diálogos modales** para confirmar acciones destructivas (cerrar puente);
  sustituyen la doble pulsación de `/stop`, que se mantiene como atajo
  equivalente.
- **Avisos** temporales (4 s, apilables, esquina inferior derecha): otro rol
  conectado/desconectado, mensaje rechazado, copia al portapapeles,
  reconexión del watch, puente cerrado.

## 7. Datos de estado

Interfaz opcional nueva, comprobada con type assertion:

    type StatusProvider interface {
        Status(ctx) (BridgeStatus, error) // modo, peer, inactividad, inicio, cwd
    }

`ControlTransport` la implementa con `/v1/health` (sondeo cada 2 s);
`clientTransport` con los datos locales del `Client` y callbacks que el modo
le pasa (p. ej. `Server.WorkerConnected`). Si un transporte no la implementa,
el panel muestra solo lo deducible de los eventos.

## 8. Tema

- Oscuro y claro; por defecto según el fondo de la terminal
  (desde v0.4.0: `tea.RequestBackgroundColor` dentro del programa, arrancando en oscuro; antes `lipgloss.HasDarkBackground`, que leía stdin fuera de Bubble Tea). `--theme dark|light|auto` en `tui`, `local`,
  host y `join`, y variable `CODEX_BRIDGE_THEME`.
- Paleta con colores semánticos (orquestador, ejecutor, humano, estados,
  borde activo/inactivo, avisos) y contraste verificado en ambos temas.
- Sin colores: respetar `NO_COLOR` (degradar a atributos y símbolos).

## 9. Compatibilidad y seguridad

- La vista normal nunca muestra tokens ni la capability (las pruebas actuales
  de seguridad se conservan, adaptadas a los nuevos componentes).
- F5 y `/pair` del host y el fallback del comando de unión en la terminal
  siguen igual.
- La observadora sigue sin confirmar mensajes.
- Máximo de mensajes renderizados: el historial de RAM existente (1.000).

## 10. Pruebas

1. Componentes: unitarias de `Update`/`View` (tarjeta, etiqueta, estados,
   plegado, composer con etiqueta e historial, paleta con filtro, diálogo,
   avisos con expiración).
2. Instantáneas de vista (golden) a 60, 80 y 120 columnas, en ambos temas,
   con perfil de color fijo para que sean deterministas.
3. Mapa de atajos: sin colisiones por contexto; la ayuda lista todos.
4. Capacidades: cada modo expone exactamente la matriz de §4.
5. Pantalla de inicio con una fuente de puentes falsa: listado, filtro,
   cerrar con confirmación, crear, estado vacío.
6. Markdown: código resaltado, caché por ancho y tema, degradación sin color.
7. Regresión: seguridad del token, ACK por modo, observadora sin consumir,
   `/stop` y cierre según capacidades.

Verificación: `gofmt -l .`, `go vet ./...`, `go test -race -count=3 ./...`,
builds darwin/arm64 y windows/amd64; revisión manual en la terminal del
usuario en Mac y en Windows (Windows Terminal y PowerShell 5.1).

## 11. Fases

1. **Base:** tema, atajos, shell, barra de estado, tarjetas (sin Markdown),
   composer con etiqueta e historial, ayuda, capacidades; los cuatro modos
   migrados a la nueva interfaz.
2. **Contenido:** glamour y resaltado, plegado, selección y copia, búsqueda,
   panel lateral con `StatusProvider`, avisos, paleta y diálogos.
3. **Inicio:** pantalla de lista de puentes y navegación inicio ↔ puente.
