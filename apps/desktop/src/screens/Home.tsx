import { useState } from 'react';
import type { Instance } from '../api/types';
import { localRole, roleName, roleState } from '../model';
export default function Home({ instances, loading, creating, onOpen, onCreate, onClose }: { instances: Instance[]; loading: boolean; creating: boolean; onOpen(i: Instance): void; onCreate(): void; onClose(i: Instance): void }) {
  const [filter, setFilter] = useState('');
  const shown = instances.filter(i => `${i.instance_id} ${i.project} ${i.mode}`.toLowerCase().includes(filter.toLowerCase()));
  return <section className="home" aria-labelledby="home-title">
    <div className="section-heading"><div><p className="eyebrow">ESPACIO DE TRABAJO</p><h1 id="home-title">Tus puentes</h1><p className="subtitle">Sigue la conversación. Intervén cuando haga falta.</p></div><button className="primary" disabled={creating} onClick={onCreate}>＋ {creating ? 'Creando…' : 'Nuevo puente local'}</button></div>
    <div className="list-toolbar"><label className="search"><span aria-hidden>⌕</span><input aria-label="Filtrar puentes" placeholder="Filtrar por proyecto o instancia…" value={filter} onChange={e => setFilter(e.target.value)} /></label><span>{instances.length} puentes · actualización cada 2 s</span></div>
    <div className="bridge-list">{shown.map(i => <article className="bridge-card" key={i.instance_id}>
      <div className="bridge-top"><span className={i.peer_connected ? 'connection connected' : 'connection'}>{i.peer_connected ? '● Conectado' : '○ Sin compañero'}</span><span className="mode">{i.mode === 'local' ? 'Local' : i.mode}</span></div>
      <button className="project-button" onClick={() => onOpen(i)} aria-label={`Abrir ${i.project || i.instance_id}`}><h2>{i.project || 'Sin proyecto'}</h2><span className="instance-id">{i.instance_id.slice(0, 10)} <span aria-hidden>↗</span></span></button>
      <div className="card-roles">{(['orchestrator', 'executor'] as const).map(r => { const s = roleState(i, r); return <div key={r}><span className={`role ${r}`}>{roleName(r)}</span><span>{s?.state || '—'}{s?.tool ? ` · ${s.tool}` : ''}</span></div>; })}</div>
      <div className="card-footer"><span>{i.latest_server_seq} mensajes · inactivo {i.idle_seconds ?? 0} s</span><span>{roleName(localRole(i))} local</span><button className="text-button" aria-label={`Cerrar ${i.project || i.instance_id}`} onClick={() => onClose(i)}>Cerrar</button></div>
    </article>)}</div>
    {loading && !instances.length && <p role="status">Buscando puentes…</p>}
    {!loading && !shown.length && <div className="empty"><h2>{filter ? 'Sin coincidencias' : 'No hay puentes activos'}</h2><p>{filter ? 'Prueba otro proyecto o instancia.' : 'Crea un puente local para reunir a tus agentes.'}</p></div>}
    <aside className="home-note"><span aria-hidden>↔</span><div><strong>El humano también tiene voz</strong><p>Los mensajes que envíes desde aquí se identifican como intervención humana.</p></div></aside>
  </section>;
}
