import { Send, UserRound, Keyboard } from 'lucide-react';
import { useState } from 'react';
import type { Label } from '../api/types';
import { labels } from '../model';
export default function Composer({ onSend, disabled }: { onSend(body: string, label: Label): Promise<void>; disabled: boolean }) {
  const [body, setBody] = useState(''); const [label, setLabel] = useState<Label>('RESPUESTA'); const [history, setHistory] = useState<{ body: string; label: Label }[]>([]); const [busy, setBusy] = useState(false); const [error, setError] = useState('');
  async function send() { if (!body.trim() || busy || disabled) return; setBusy(true); setError(''); const draft = body; try { await onSend(draft, label); setHistory(h => [{ body: draft, label }, ...h].slice(0, 20)); setBody(''); } catch (e) { setError(String(e)); } finally { setBusy(false); } }
  return <form className="composer" onSubmit={e => { e.preventDefault(); void send(); }} aria-label="Enviar intervención humana">
    <div className="composer-toolbar"><span className="human"><UserRound size={13} aria-hidden="true"/>Intervención humana</span><label>Etiqueta <select aria-label="Etiqueta del mensaje" value={label} onChange={e => setLabel(e.target.value as Label)}>{labels.map(l => <option key={l}>{l}</option>)}</select></label><label className="history-label">Historial <select aria-label="Historial de mensajes" value="" onChange={e => { const item = history[Number(e.target.value)]; if (item) { setBody(item.body); setLabel(item.label); } }}><option value="">Recuperar…</option>{history.map((h, n) => <option key={n} value={n}>{h.body.slice(0, 50)}</option>)}</select></label></div>
    <div className="compose-input"><textarea aria-label="Mensaje" placeholder="Escribe para los agentes…" maxLength={250000} value={body} disabled={disabled || busy} onChange={e => setBody(e.target.value)} onKeyDown={e => { if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') { e.preventDefault(); void send(); } }} /><button className="primary" type="submit" disabled={disabled || busy || !body.trim()}><span>{busy?'Enviando…':'Enviar'}</span><Send size={16} aria-hidden="true"/></button></div>
    <div className="composer-hint"><span><Keyboard size={11} aria-hidden="true"/>Cmd / Ctrl + Enter para enviar · los agentes verán el origen humano</span>{error && <span role="alert" className="error-text">{error}</span>}</div>
  </form>;
}
