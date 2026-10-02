import { relativeTime, shortInstance } from '../format';
import { useState } from 'react';
import { Plus, Search, Copy, ArrowUpRight, Clock3, MessagesSquare, ArrowLeftRight } from 'lucide-react';
import type { Instance } from '../api/types';
import { localRole, roleName, roleState } from '../model';
import BoardPreview, { type BoardPreviews } from '../components/BoardPreview';
import RoleAvatar from '../components/RoleAvatar';
export default function Home({instances,loading,creating,onOpen,onCreate,onClose,onCopy,previews={}}:{instances:Instance[];loading:boolean;creating:boolean;onOpen(i:Instance):void;onCreate():void;onClose(i:Instance):void;onCopy(i:Instance):void;previews?:BoardPreviews}){
 const [filter,setFilter]=useState('');
 const shown=instances.filter(i=>`${i.instance_id} ${i.project} ${i.mode}`.toLowerCase().includes(filter.toLowerCase()));
 return <section className="home" aria-labelledby="home-title">
  <div className="section-heading"><div><p className="eyebrow">TU ESPACIO DE COLABORACIÓN</p><h1 id="home-title">Tus puentes</h1><p className="subtitle">Todas las conversaciones entre tus agentes.</p></div><span className="workspace-count">{instances.length} puentes activos</span></div>
  <div className="list-toolbar"><label className="search"><Search size={17} aria-hidden="true"/><input aria-label="Filtrar puentes" placeholder="Buscar por proyecto o instancia…" value={filter} onChange={e=>setFilter(e.target.value)}/></label><span><span className="live-dot"/>Actualización cada 2 s</span></div>
  <div className="bridge-list"><button className="create-card" disabled={creating} onClick={onCreate}><span className="create-plus"><Plus size={30} strokeWidth={1.7} aria-hidden="true"/></span><strong>{creating?'Creando…':'Nuevo puente local'}</strong><span>Abre un espacio para tus agentes</span></button>
   {shown.map(i=><article className="bridge-card" key={i.instance_id}>
    <button className="board-open" onClick={()=>onOpen(i)} aria-label={`Abrir ${i.project||i.instance_id}`}><BoardPreview notes={previews[i.instance_id]}/><span className="board-overlay"><span className="mode">{i.mode==='local'?'Local':i.mode}</span><ArrowUpRight size={16} aria-hidden="true"/></span><span className="project-title">{i.project||'Sin proyecto'}</span></button>
    <div className="card-details"><div className="card-id"><button className="copy-id instance-id" aria-label={`Copiar ID de ${i.project||i.instance_id}`} title="Copiar ID completo" onClick={()=>onCopy(i)}>{shortInstance(i.instance_id)}<Copy size={11} aria-hidden="true"/></button><span className={i.peer_connected?'connection connected':'connection'}><span className="live-dot"/>{i.peer_connected?'Conectado':'Sin compañero'}</span></div>
     <div className="card-roles">{(['orchestrator','executor'] as const).map(r=>{const s=roleState(i,r);return <div key={r}><RoleAvatar role={r} connected={i.roles.includes(r)&& (r===localRole(i)||i.peer_connected)}/><div className="card-role-info"><span className={`role ${r}`}>{roleName(r)}</span><span>{s?.state||'—'}{s?.tool?` · ${s.tool}`:''}</span></div><time title={`Latido ${relativeTime(s?.last_heartbeat_at)}`} dateTime={s?.last_heartbeat_at}>{relativeTime(s?.last_heartbeat_at)}</time></div>;})}</div>
     <div className="card-footer"><span><MessagesSquare size={12} aria-hidden="true"/>{i.latest_server_seq} mensajes</span><span><Clock3 size={12} aria-hidden="true"/>{i.idle_seconds??0} s inactivo</span><button className="text-button" aria-label={`Cerrar ${i.project||i.instance_id}`} onClick={()=>onClose(i)}>Cerrar</button></div>
    </div>
   </article>)}
  </div>
  {loading&&!instances.length&&<p role="status">Buscando puentes…</p>}
  {!loading&&!shown.length&&<div className="empty"><h2>{filter?'Sin coincidencias':'Todo empieza con una conversación'}</h2><p>{filter?'Prueba otro proyecto o instancia.':'Crea tu primer puente para reunir a tus agentes.'}</p></div>}
  <aside className="home-note"><ArrowLeftRight size={18} aria-hidden="true"/><p>Tu voz también cuenta. Intervén en cualquier conversación como humano.</p></aside>
 </section>;
}
