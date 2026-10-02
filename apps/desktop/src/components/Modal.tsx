import { useEffect, useId, useRef, type ReactNode } from 'react';
import type { LucideIcon } from 'lucide-react';
import { X } from 'lucide-react';
import IconButton from './IconButton';
export default function Modal({ title, onCancel, children, busy = false, headerIcon: HeaderIcon, closeLabel }: {title:string; onCancel():void; children:ReactNode; busy?:boolean; headerIcon?:LucideIcon; closeLabel?:string}) {
  const previous=useRef(document.activeElement as HTMLElement); const ref=useRef<HTMLDialogElement>(null); const titleId=useId(); const dismiss=useRef(onCancel); dismiss.current=onCancel;
  useEffect(()=>{const dialog=ref.current!; dialog.showModal(); dialog.querySelector<HTMLElement>('[autofocus],button,input,select')?.focus(); return()=>previous.current?.focus();},[]);
  return <dialog ref={ref} aria-labelledby={titleId} onCancel={e=>{e.preventDefault();if(!busy)dismiss.current();}} onKeyDown={e=>{
    if(e.key==='Escape'){e.preventDefault();e.stopPropagation();if(!busy)dismiss.current();}
    if(e.key==='Tab'){
      const items=Array.from(ref.current!.querySelectorAll<HTMLElement>('button:not(:disabled),a[href],input:not(:disabled),select:not(:disabled),textarea:not(:disabled),[tabindex="0"]'));
      const first=items[0],last=items.at(-1); if(!first){e.preventDefault();return;}
      if(e.shiftKey && document.activeElement===first){e.preventDefault();last?.focus();}
      else if(!e.shiftKey && document.activeElement===last){e.preventDefault();first.focus();}
    }
  }}><div className="dialog-heading">{HeaderIcon && <HeaderIcon size={16} aria-hidden="true"/>}<h2 id={titleId}>{title}</h2>{closeLabel && <IconButton icon={X} label={closeLabel} className="dialog-close" type="button" onClick={() => dismiss.current()} disabled={busy}/>}</div>{children}</dialog>;
}
