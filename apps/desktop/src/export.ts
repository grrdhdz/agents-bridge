import { save, type SaveDialogOptions } from '@tauri-apps/plugin-dialog';
import type { ApiClient } from './api/client';
export async function exportConversation(client:ApiClient,instance:string,format:'md'|'jsonl',pick:(options:SaveDialogOptions)=>Promise<string|null>=save){
  const ext=format==='md'?'md':'jsonl';
  const path=await pick({title:'Exportar conversación',defaultPath:`agents-bridge-${instance}.${ext}`,filters:[{name:format==='md'?'Markdown':'JSONL',extensions:[ext]}]});
  if(!path)return null;
  await client.export({instance_id:instance,format,output:path});return path;
}
