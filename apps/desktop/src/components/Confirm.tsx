import Modal from './Modal';
import { shortInstance } from '../format';
export default function Confirm({ name, busy, error, onCancel, onConfirm }: {name:string;busy:boolean;error?:string;onCancel():void;onConfirm():void}) {
  return <Modal title="¿Cerrar este puente?" busy={busy} onCancel={onCancel}><p>La instancia <code>{shortInstance(name)}</code> se cerrará para ambos agentes. Los mensajes solo viven en memoria.</p>
    {error&&<p role="alert" className="error-text">{error}</p>}<div className="dialog-actions"><button autoFocus disabled={busy} onClick={onCancel}>Cancelar</button><button className="danger-button" disabled={busy} onClick={onConfirm}>{busy?'Cerrando…':'Cerrar puente'}</button></div>
  </Modal>;
}
