import { Search, ArrowUpRight } from 'lucide-react';
import { Command } from 'cmdk';
export type CommandAction = {id:string; label:string; run():void; disabled?:boolean};
export default function CommandPalette({open,onOpenChange,actions}:{open:boolean;onOpenChange(open:boolean):void;actions:CommandAction[]}) {
  return <Command.Dialog open={open} onOpenChange={onOpenChange} label="Paleta de comandos" overlayClassName="command-overlay" contentClassName="command-dialog" loop>
    <div className="command-search"><Search size={20} aria-hidden="true"/><Command.Input placeholder="Escribe una acción…" aria-label="Buscar comando"/></div>
    <Command.List aria-label="Acciones"><Command.Empty>Sin coincidencias</Command.Empty>{actions.map(a=><Command.Item key={a.id} value={a.label} disabled={a.disabled} onSelect={()=>{onOpenChange(false);a.run();}}><span>{a.label}</span><ArrowUpRight size={14} aria-hidden="true"/></Command.Item>)}</Command.List>
    <p className="command-hint">↑ ↓ para elegir · Enter para ejecutar · Esc para cancelar</p>
  </Command.Dialog>;
}
