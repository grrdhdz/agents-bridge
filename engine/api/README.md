# API v1 de agents-bridge

El motor expone `agents-bridge api`: proceso de larga vida sin listener nuevo,
JSON por líneas UTF-8 en stdin/stdout. stdout contiene únicamente paquetes del
contrato [schema.json](schema.json); stderr se reserva para fallos del proceso.
La app no necesita acceder a ningún archivo interno ni importar código Go.

Mantén stdin abierto mientras esperas respuestas/eventos. EOF cancela peticiones
en curso y suscripciones, libera conexiones y termina; nunca detiene puentes.
Una respuesta ya construida puede salir antes del cierre. Un consumidor que
bloquea stdout durante el cierre se desconecta después de un segundo. Cada
petición tiene presupuesto de 25 s; el lanzamiento espera ready hasta 20 s.
Límite por línea: 2 MiB. Una línea JSON inválida devuelve INVALID_REQUEST y el
proceso continúa; una línea que supera el límite termina el proceso con error.

## Paquetes

Petición (id no vacío, máximo 128 caracteres, args siempre objeto):

```json
{"v":1,"id":"hello-1","op":"hello","args":{}}
```

Respuesta (id correlaciona con la petición; id vacío si no se pudo decodificar):

```json
{"v":1,"id":"hello-1","ok":true,"result":{"engine_version":"v0.4.0","contract_version":1}}
```

```json
{"v":1,"id":"health-1","ok":false,"error":{"code":"INSTANCE_NOT_FOUND","message":"No hay un puente disponible con esa instancia."}}
```

Evento independiente de cualquier petición:

```json
{"v":1,"sub":"sub-…","event":"delivery","data":{"instance_id":"…","event_seq":7,"message_id":"…","status":"delivered"}}
```

## Operaciones

| op | args | result |
|---|---|---|
| hello | `{}` | engine_version y contract_version=1. La versión del motor corresponde al binario ejecutado; no al nombre de la rama. |
| list | `{}` | instances: datos públicos de ps, project como nombre base, estado por rol, hook_bound, último latido/herramienta y timestamps de actividad. |
| subscribe | `{instance_id}` | sub: id opaco. La respuesta sale antes de cualquier evento de esa suscripción. |
| unsubscribe | `{sub}` | unsubscribed. Cancela y espera los observadores; ningún evento de esa sub sale después de esta respuesta. |
| send | `{instance_id, body, label?}` | instance_id, message_id, source=human-operator y role=orchestrator/executor. |
| stop | `{instance_id}` | instance_id, state=stopping. Es la misma operación pública que stop. |
| create_local | `{idle_timeout?}` | instance_id, state=running, una vez publicado el ready. |
| health | `{instance_id}` | state, pid, peer_connected, fin_received, latest_server_seq y role_states. |
| export | `{instance_id, format, output}` | instance_id, format, output absoluto. |

Ejemplos de peticiones independientes:

```json
{"v":1,"id":"list-1","op":"list","args":{}}
{"v":1,"id":"create-1","op":"create_local","args":{"idle_timeout":"30m"}}
{"v":1,"id":"observe-1","op":"subscribe","args":{"instance_id":"INSTANCIA"}}
{"v":1,"id":"human-1","op":"send","args":{"instance_id":"INSTANCIA","label":"URGENTE","body":"Revisa este detalle."}}
{"v":1,"id":"save-1","op":"export","args":{"instance_id":"INSTANCIA","format":"md","output":"/ruta/nueva/conversacion.md"}}
{"v":1,"id":"unsubscribe-1","op":"unsubscribe","args":{"sub":"SUB"}}
```

- idle_timeout usa duraciones Go/CLI no negativas (`20s`, `30m`, `0s`);
  ausencia mantiene el valor por defecto de local. 0 desactiva el cierre.
- label acepta TAREA, PREGUNTA, RESPUESTA, RESULTADO, FIN, URGENTE, PROGRESO.
  Se antepone como primera línea. Sin label se conserva body completo.
  No se acepta source ni role del cliente: siempre intervención humana, desde
  el endpoint orquestador si existe en este equipo, si no el disponible.
- export acepta md/jsonl. Reserva un archivo nuevo con permisos privados;
  nunca sobrescribe. Usa el mismo exportador que ctl, sin confirmar mensajes.
  El archivo avisa si el journal en RAM expulsó eventos anteriores.
- create_local usa el mismo lanzador desacoplado que la pantalla de inicio de
  la TUI, con stdin/stdout desconectados y ready privado. Un lanzamiento fallido
  limpia su hijo; un puente creado correctamente sobrevive al cierre de api.

## Observación y orden

subscribe usa el watch público del plano de control, replay desde event_seq=0
y reconexión compartida con la TUI. No ACK, no wait y ningún cursor de consumo.
Los mensajes del agente siguen pendientes hasta que su propio ctl wait los lee.
No hay snapshots de mensajes guardados por la API; el journal sigue en RAM.
Máximo ocho suscripciones por proceso, además del límite del endpoint.

Eventos: message (message con envelope público), delivery (accepted/delivered),
state, transport, lifecycle. Los eventos del journal conservan event_seq y su
orden por suscripción. Una instantánea de health aparece como state con
`data.health`, sin event_seq; se sondea cada segundo y solo se emite al cambiar.
No usar esa instantánea como cursor del journal. El replay truncado conserva
solo lo que queda, igual que la TUI, sin inventar mensajes históricos.

## Seguridad y versionado

El contrato no contiene capabilities, pairing/reconnect tokens, control_url ni
rutas de descriptores. Los errores públicos son mensajes fijos, no errores de
red/archivos sin filtrar. Se omiten los secretos/rutas privados conocidos y se
recortan credenciales con sus codificaciones conocidas si aparecen en textos
de mensajes/herramientas. La exportación API aplica el mismo filtro; ctl conserva
su comportamiento anterior. La interfaz no debe intentar reconstruir secretos.

No se añaden listeners; solo el proceso Go accede a descriptores y autentica
loopback. Los bridges, el observador, el exportador y el lanzador son compartidos
con las herramientas existentes. El núcleo no depende de internal/api ni la TUI.

v=1 identifica esta forma de paquetes. Cambios incompatibles requieren otra
versión; campos nuevos requieren revisar schema y regenerar tipos de las apps.
El esquema enumera peticiones/resultados de cada operación y variantes de eventos.
Su validador Go estructural implementa solo los keywords publicados, rechaza
keywords desconocidos y valida los paquetes de los tests sin dependencias nuevas.

`health` incluye `unread` cuando el peek del endpoint está disponible: cuenta
la bandeja del rol local sin consumirla. Es opcional para mantener compatibilidad
con motores v1 anteriores y omitir un dato desconocido si peek falla.
