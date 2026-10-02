import Modal from './Modal';
export default function ShortcutHelp({onClose}:{onClose():void}){
  return <Modal title="Atajos de teclado" onCancel={onClose}><dl className="shortcut-list"><div><dt>Cmd/Ctrl + K</dt><dd>Paleta de comandos</dd></div><div><dt>Cmd/Ctrl + F</dt><dd>Buscar en la conversación</dd></div><div><dt>Enter / Shift + Enter</dt><dd>Siguiente / anterior coincidencia</dd></div><div><dt>Cmd/Ctrl + Enter</dt><dd>Enviar mensaje</dd></div><div><dt>Tab / Shift + Tab</dt><dd>Mover el foco</dd></div><div><dt>Esc</dt><dd>Cerrar diálogo o búsqueda</dd></div><div><dt>?</dt><dd>Esta ayuda (fuera de campos)</dd></div></dl><div className="dialog-actions"><button autoFocus onClick={onClose}>Cerrar ayuda</button></div></Modal>;
}
