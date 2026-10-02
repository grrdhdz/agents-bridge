import { useCallback, useEffect, useRef, useState } from 'react';
export type Toast = {id:number; message:string};
export function useToasts(){
  const [toasts,setToasts]=useState<Toast[]>([]); const next=useRef(0); const timers=useRef(new Map<number,ReturnType<typeof setTimeout>>());
  const dismiss=useCallback((id:number)=>{clearTimeout(timers.current.get(id));timers.current.delete(id);setToasts(t=>t.filter(x=>x.id!==id));},[]);
  const notify=useCallback((message:string)=>{const id=++next.current;setToasts(t=>[...t.slice(-3),{id,message}]);timers.current.set(id,setTimeout(()=>dismiss(id),8000));},[dismiss]);
  useEffect(()=>()=>{timers.current.forEach(clearTimeout);timers.current.clear();},[]);
  return {toasts,notify,dismiss};
}
export function Toasts({items,onDismiss}:{items:Toast[];onDismiss(id:number):void}){
  return <aside className="toast-stack" aria-label="Avisos">{items.map(t=><div className="toast" role="status" key={t.id}><span>{t.message}</span><button aria-label={`Descartar ${t.message}`} onClick={()=>onDismiss(t.id)}>×</button></div>)}</aside>;
}
