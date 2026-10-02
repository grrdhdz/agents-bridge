import { useEffect, useRef } from 'react';
export default function SearchBar({query,count,index,onQuery,onMove,onClose}:{query:string;count:number;index:number;onQuery(q:string):void;onMove(direction:number):void;onClose():void}) {
  const ref=useRef<HTMLInputElement>(null);
  useEffect(()=>{ref.current?.focus();ref.current?.select();},[]);
  return <div className="conversation-search" role="search"><input ref={ref} aria-label="Buscar en la conversación" placeholder="Buscar en la conversación…" value={query} onChange={e=>onQuery(e.target.value)} onKeyDown={e=>{if(e.key==='Escape'){e.stopPropagation();onClose();}if(e.key==='Enter'){e.preventDefault();onMove(e.shiftKey?-1:1);}}}/><span role="status">{count?`${index+1} de ${count} mensajes`:'Sin coincidencias'}</span><button aria-label="Anterior coincidencia" disabled={!count} onClick={()=>onMove(-1)}>↑</button><button aria-label="Siguiente coincidencia" disabled={!count} onClick={()=>onMove(1)}>↓</button><button aria-label="Cerrar búsqueda" onClick={onClose}>×</button></div>;
}
