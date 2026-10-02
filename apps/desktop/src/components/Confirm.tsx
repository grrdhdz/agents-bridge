import { useEffect, useRef } from 'react';
export default function Confirm({ name, busy, error, onCancel, onConfirm }: { name: string; busy: boolean; error?: string; onCancel(): void; onConfirm(): void }) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => { const previous = document.activeElement as HTMLElement; ref.current?.showModal(); return () => previous?.focus(); }, []);
  return <dialog ref={ref} aria-labelledby="close-title" onCancel={e => { e.preventDefault(); if (!busy) onCancel(); }}>
    <h2 id="close-title">¿Cerrar este puente?</h2><p>La instancia <code>{name.slice(0, 10)}</code> se cerrará para ambos agentes. Los mensajes solo viven en memoria.</p>
    {error && <p role="alert" className="error-text">{error}</p>}<div className="dialog-actions"><button autoFocus disabled={busy} onClick={onCancel}>Cancelar</button><button className="danger-button" disabled={busy} onClick={onConfirm}>{busy ? 'Cerrando…' : 'Cerrar puente'}</button></div>
  </dialog>;
}
