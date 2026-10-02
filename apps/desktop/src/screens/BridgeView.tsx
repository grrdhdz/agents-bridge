import { useEffect, useRef, useState } from 'react';
import type { ApiClient } from '../api/client';
import type { Instance, Event, Health, Role, Label, Message } from '../api/types';
import SearchBar from '../components/SearchBar';
import { PanelRightClose, PanelRightOpen } from 'lucide-react';
import IconButton from '../components/IconButton';
import type { PreviewNote } from '../components/BoardPreview';
import { applyEvent, bodyParts, localRole, messageRole, type ChatState } from '../model';
import MessageCard from '../components/MessageCard';
import Composer from '../components/Composer';
import Sidebar from '../components/Sidebar';

export default function BridgeView({client,instance,onToast,searchRequest,onPreview}:{client:ApiClient;instance:Instance;onToast(message:string):void;searchRequest:number;onPreview(id:string,notes:PreviewNote[]):void}) {
  const [chat, setChat] = useState<ChatState>({ messages: [], statuses: {} });
  const [health, setHealth] = useState<Health>(); const [role, setRole] = useState<Role>(localRole(instance)); const [error, setError] = useState(''); const [healthError, setHealthError] = useState(''); const [closed, setClosed] = useState(false);
  const [panelOpen,setPanelOpen]=useState(true);
  const [searchOpen,setSearchOpen]=useState(false); const [query,setQuery]=useState(''); const [searchIndex,setSearchIndex]=useState(0);
  const toast=useRef(onToast); toast.current=onToast;
  const closedNotice=useRef(false);
  const rejected=useRef(new Set<string>());
  const list = useRef<HTMLDivElement>(null); const follow = useRef(true);
  useEffect(() => {
    let active = true; let sub = ''; let off: (() => void) | undefined; const buffered: Event[] = [];
    const receive = (event: Event) => { if (!active || event.sub !== sub) return; setChat(s => applyEvent(s, event, localRole(instance))); if (event.data.health) setHealth(event.data.health); if (event.event === 'lifecycle' && event.data.state === 'closed') setClosed(true); if(event.event==='delivery' && event.data.status==='rejected' && !rejected.current.has(event.data.message_id)){rejected.current.add(event.data.message_id);toast.current('Mensaje rechazado por el motor');} };
    let subscribing = false;
    async function subscribe() {
      if (!active || subscribing || sub) return;
      subscribing = true;
      try {
        const result = await client.subscribe({ instance_id: instance.instance_id }); sub = result.sub;
        if (!active) { await client.unsubscribe({ sub }); return; }
        buffered.forEach(receive); buffered.length = 0; setError('');
      } catch (e) { if (active) { setError(String(e)); if (String(e).includes('INSTANCE_NOT_FOUND')) setClosed(true); } }
      finally { subscribing = false; }
    }
    let retry: ReturnType<typeof setInterval> | undefined;
    void (async () => {
      try {
        off = await client.onEvent(event => { if (event.data.instance_id !== instance.instance_id || !active) return; if (!sub) buffered.push(event); else receive(event); });
        if (!active) { off(); return; }
        await subscribe();
        if (active) retry = setInterval(() => { if (!sub) void subscribe(); }, 1000);
      } catch (e) { if (active) setError(String(e)); }
    })();
    let reading = false;
    const refresh = async () => { if (reading) return; reading = true; try { const h = await client.health({ instance_id: instance.instance_id }); if (active) { setHealth(h); setHealthError(''); setClosed(h.state === 'closed'); } } catch (e) { if (active) { setHealthError(String(e)); if (String(e).includes('INSTANCE_NOT_FOUND')) setClosed(true); } } finally { reading = false; } };
    void refresh(); const timer = setInterval(() => void refresh(), 2000);
    return () => { active = false; clearInterval(timer); clearInterval(retry); off?.(); if (sub) void client.unsubscribe({ sub }).catch(() => {}); };
  }, [client, instance.instance_id]);
  useEffect(() => { if (!searchOpen && follow.current && list.current) list.current.scrollTop = list.current.scrollHeight; }, [chat.messages, searchOpen]);
  useEffect(()=>{if(searchRequest){setSearchOpen(true);follow.current=false;}},[searchRequest]);
  useEffect(()=>{if(closed&&!closedNotice.current){closedNotice.current=true;toast.current('Este puente está cerrado');}},[closed]);
  const matches=query.trim()?chat.messages.filter(m=>m.body.toLowerCase().includes(query.trim().toLowerCase())).map(m=>m.message_id):[];
  const selectedMatch=matches.length?matches[searchIndex % matches.length]:undefined;
  useEffect(()=>{if(selectedMatch)list.current?.querySelectorAll<HTMLElement>('[data-message-id]').forEach(el=>{if(el.dataset.messageId===selectedMatch)el.scrollIntoView({block:'center',behavior:'instant'});});},[selectedMatch,query]);
  function closeSearch(){setSearchOpen(false);setQuery('');list.current?.focus();}
  useEffect(()=>{if(chat.messages.length)onPreview(instance.instance_id,chat.messages.slice(-4).map(m=>({role:messageRole(m),human:m.source==='human-operator',label:bodyParts(m.body).label})));},[chat.messages,instance.instance_id,onPreview]);
  async function send(body: string, label: Label) {
    const draftId = `draft-${crypto.randomUUID()}`;
    const draft: Message = { protocol_version: 1, instance_id: instance.instance_id, message_id: draftId, client_seq: 0, server_seq: 0, sender_id: 'human', sender_role: role === 'orchestrator' ? 'mac-orchestrator' : 'win-executor', kind: 'text', body: `${label}\n${body}`, body_sha256: '', source: 'human-operator', created_at: new Date().toISOString(), accepted_at: '' };
    setChat(s => ({ messages: [...s.messages, draft], statuses: { ...s.statuses, [draftId]: 'queued-ram' } }));
    try {
      const result = await client.send({ instance_id: instance.instance_id, body, label }); setRole(result.role);
      setChat(s => { const messages = s.messages.filter(m => m.message_id !== draftId); if (!messages.some(m => m.message_id === result.message_id)) messages.push({ ...draft, message_id: result.message_id, sender_role: result.role === 'orchestrator' ? 'mac-orchestrator' : 'win-executor' }); return { messages, statuses: { ...s.statuses, [result.message_id]: s.statuses[result.message_id] || 'accepted' } }; });
    } catch (e) { setChat(s => ({ ...s, statuses: { ...s.statuses, [draftId]: 'rejected' } })); toast.current('Mensaje rechazado. Se conserva el borrador.'); throw e; }
  }
  const sent = chat.messages.filter(m => messageRole(m) === role);
  const pending = sent.filter(m => ['queued-ram', 'accepted'].includes(chat.statuses[m.message_id])).length;
  return <section className="bridge-view" aria-label="Conversación del puente">
    <IconButton className={`panel-toggle ${panelOpen?'':'folded'}`} icon={panelOpen?PanelRightClose:PanelRightOpen} label={panelOpen?'Plegar panel lateral':'Mostrar panel lateral'} aria-controls="bridge-sidebar" aria-expanded={panelOpen} onClick={()=>setPanelOpen(!panelOpen)}/>

    <div className={`bridge-layout ${panelOpen?'':'panel-folded'}`}><div className="chat-column">{searchOpen&&<SearchBar query={query} count={matches.length} index={matches.length?searchIndex%matches.length:0} onQuery={q=>{setQuery(q);setSearchIndex(0);}} onMove={direction=>setSearchIndex(n=>matches.length?(n+direction+matches.length)%matches.length:0)} onClose={closeSearch}/>}{(error || healthError || closed) && <p role="alert" className="view-error">{closed ? 'Este puente está cerrado.' : error || healthError}</p>}<div className="conversation" ref={list} tabIndex={0} role="log" aria-label="Mensajes" aria-live="polite" onScroll={e => { const el = e.currentTarget; follow.current = el.scrollHeight - el.clientHeight - el.scrollTop < 60; }}><p className="conversation-date">{new Date().toLocaleDateString('es', { day: 'numeric', month: 'long' })} · conversación entre agentes</p>{chat.messages.map(m => <MessageCard key={m.message_id} message={m} status={chat.statuses[m.message_id] || 'queued-ram'} local={role} query={searchOpen?query.trim():''} selected={m.message_id===selectedMatch} />)}{!chat.messages.length && <div className="empty"><h2>Esperando mensajes</h2><p>La conversación aparecerá aquí sin consumir la bandeja de los agentes.</p></div>}</div><Composer disabled={closed} onSend={send} /></div>{panelOpen&&<Sidebar instance={instance} health={health} total={chat.messages.length} sent={sent.length} pending={pending} local={role} />}</div>
  </section>;
}
