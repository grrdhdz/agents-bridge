import { useCallback, useEffect, useRef, useState } from 'react';
import { realClient, type ApiClient, type EngineStatus } from './api/client';
import type { HelloResult, Instance } from './api/types';
import { applyTheme, type ThemeMode } from './theme';
import { localRole, roleName } from './model';
import { exportConversation } from './export';
import Home from './screens/Home';
import BridgeView from './screens/BridgeView';
import Confirm from './components/Confirm';
import CommandPalette, { type CommandAction } from './components/CommandPalette';
import ShortcutHelp from './components/ShortcutHelp';
import { Toasts, useToasts } from './components/Toasts';

export default function App({ client = realClient, demo = false }: { client?: ApiClient; demo?: boolean }) {
  const [engine, setEngine] = useState<HelloResult>();
  const [instances, setInstances] = useState<Instance[]>([]);
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<Instance>();
  const [confirm, setConfirm] = useState<Instance>();
  const [creating, setCreating] = useState(false);
  const [stopping, setStopping] = useState(false);
  const [closeError, setCloseError] = useState('');
  const [notice, setNotice] = useState('');
  const [loadError, setLoadError] = useState('');
  const [status, setStatus] = useState<EngineStatus>();
  const [theme, setTheme] = useState<ThemeMode>('auto');
  const [palette, setPalette] = useState(false);
  const [help, setHelp] = useState(false);
  const [searchRequest, setSearchRequest] = useState(0);
  const { toasts, notify, dismiss } = useToasts();
  const generation = useRef(0), active = useRef(true);
  const known = useRef<Instance[] | undefined>(undefined);
  const lastStatus = useRef<EngineStatus['status'] | undefined>(undefined);
  const refresh = useCallback(async () => {
    const n = ++generation.current;
    try {
      const result = await client.list();
      if (active.current && n === generation.current) {
        for (const previous of known.current || []) {
          const next = result.instances.find(i => i.instance_id === previous.instance_id);
          if (!next) notify(`Puente ${previous.project || previous.instance_id} cerrado`);
          else if (next.peer_connected !== previous.peer_connected) notify(`${roleName(localRole(next) === 'orchestrator' ? 'executor' : 'orchestrator')} ${next.peer_connected ? 'conectado' : 'desconectado'} · ${next.project || next.instance_id}`);
        }
        known.current = result.instances;
        setInstances(result.instances); setLoadError('');
      }
      return result.instances;
    } catch (e) { if (active.current) setLoadError(String(e)); return []; }
    finally { if (active.current) setLoading(false); }
  }, [client, notify]);
  useEffect(() => {
    active.current = true; known.current = undefined;
    let off: (() => void) | undefined; let busy = false;
    void (async () => {
      try {
        off = await client.onStatus(s => {
          if (!active.current) return;
          if (s.status === 'restarting' && lastStatus.current !== 'restarting') notify('Reconectando con el motor…');
          if (s.status === 'connected' && lastStatus.current === 'restarting') notify('Motor reconectado; suscripciones recuperadas');
          lastStatus.current = s.status; setStatus(s);
          if (s.status === 'connected') void client.hello().then(r => { if (active.current) setEngine(r); }).catch(() => {});
        });
        if (!active.current) { off(); return; }
        const result = await client.hello(); if (active.current) setEngine(result);
      } catch (e) { if (active.current) setNotice(String(e)); }
    })();
    const load = async () => { if (busy) return; busy = true; await refresh(); busy = false; };
    void load(); const timer = setInterval(() => void load(), 2000);
    return () => { active.current = false; clearInterval(timer); off?.(); };
  }, [client, refresh, notify]);
  useEffect(() => {
    applyTheme(theme); const mq = window.matchMedia('(prefers-color-scheme: dark)');
    const update = () => applyTheme(theme); mq.addEventListener('change', update);
    return () => mq.removeEventListener('change', update);
  }, [theme]);
  const current = instances.find(i => i.instance_id === selected?.instance_id) || selected;
  useEffect(() => {
    const down = (e: KeyboardEvent) => {
      if (confirm || help) return;
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); setPalette(p => !p); }
      else if (current && !palette && (e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'f') { e.preventDefault(); setSearchRequest(n => n + 1); }
      else if (e.key === '?' && !palette && !(e.target instanceof HTMLElement && (e.target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(e.target.tagName)))) { e.preventDefault(); setHelp(true); }
    };
    window.addEventListener('keydown', down); return () => window.removeEventListener('keydown', down);
  }, [confirm, help, current, palette]);
  async function create() {
    if (creating) return; setCreating(true);
    try { const result = await client.createLocal({}); const list = await refresh(); const instance = list.find(i => i.instance_id === result.instance_id); if (instance) setSelected(instance); else setNotice('Puente creado. La lista se actualizará en unos segundos.'); }
    catch (e) { notify(String(e)); } finally { setCreating(false); }
  }
  function askClose(i: Instance) { setCloseError(''); setConfirm(i); }
  async function stop() {
    if (!confirm || stopping) return; setStopping(true); setCloseError('');
    try { await client.stop({ instance_id: confirm.instance_id }); if (selected?.instance_id === confirm.instance_id) setSelected(undefined); setConfirm(undefined); await refresh(); }
    catch (e) { setCloseError(String(e)); } finally { setStopping(false); }
  }
  async function copy(i: Instance) {
    try { await navigator.clipboard.writeText(i.instance_id); notify('ID completo copiado'); }
    catch { notify('No se pudo copiar el ID. Vuelve a intentarlo.'); }
  }
  async function exportBridge(format: 'md' | 'jsonl') {
    if (!current) return;
    try { const path = await exportConversation(client, current.instance_id, format); if (path) notify('Conversación exportada'); }
    catch (e) { notify(`No se pudo exportar: ${String(e)}`); }
  }
  const actions: CommandAction[] = [
    { id: 'home', label: 'Ir a inicio', run: () => setSelected(undefined) },
    { id: 'create', label: 'Nuevo puente local', disabled: creating, run: () => void create() },
    { id: 'close', label: 'Cerrar puente', disabled: !current, run: () => current && askClose(current) },
    { id: 'copy', label: 'Copiar instance_id', disabled: !current, run: () => current && void copy(current) },
    { id: 'export-md', label: 'Exportar conversación · Markdown', disabled: !current, run: () => void exportBridge('md') },
    { id: 'export-jsonl', label: 'Exportar conversación · JSONL', disabled: !current, run: () => void exportBridge('jsonl') },
    { id: 'search', label: 'Buscar en la conversación', disabled: !current, run: () => setSearchRequest(n => n + 1) },
    ...(['light', 'dark', 'auto'] as const).map((mode, i) => ({ id: `theme-${mode}`, label: `Tema ${['claro', 'oscuro', 'automático'][i]}`, run: () => setTheme(mode) })),
    { id: 'help', label: 'Ayuda de teclado', run: () => setHelp(true) },
  ];
  return <div className="app-shell">
    <header className="app-header"><span className="brand-mark" aria-hidden>↔</span><strong className="brand">agents-bridge</strong><span className="app-name">Escritorio</span>{demo && <span className="demo-badge">Demostración</span>}
      <div className="header-right"><span className="engine-state" role="status">{status?.status === 'restarting' ? '◌ Reconectando…' : engine ? `● Motor ${engine.engine_version}` : '◌ Conectando…'}</span><button className="text-button" onClick={() => setPalette(true)} aria-label="Abrir paleta de comandos">⌘K</button><button className="text-button" onClick={() => setHelp(true)} aria-label="Ayuda de teclado">?</button><label>Tema <select aria-label="Tema" value={theme} onChange={e => setTheme(e.target.value as ThemeMode)}><option value="auto">Automático</option><option value="light">Claro</option><option value="dark">Oscuro</option></select></label></div>
    </header>
    {(notice || loadError || (status && status.status !== 'connected')) && <div className="notice" role="alert"><span>{notice || loadError || status?.message}</span>{notice && <button aria-label="Descartar aviso" onClick={() => setNotice('')}>×</button>}</div>}
    <main className={current ? 'main-bridge' : 'main-home'}>{current ? <BridgeView key={current.instance_id} client={client} instance={current} searchRequest={searchRequest} onToast={notify} onCopy={() => void copy(current)} onExport={format => void exportBridge(format)} onBack={() => { setSearchRequest(0); setSelected(undefined); }} onClose={() => askClose(current)} /> : <Home instances={instances} loading={loading} creating={creating} onOpen={setSelected} onCreate={() => void create()} onCopy={i => void copy(i)} onClose={askClose} />}</main>
    <CommandPalette open={palette} onOpenChange={setPalette} actions={actions}/>
    {help && <ShortcutHelp onClose={() => setHelp(false)}/>}
    {confirm && <Confirm name={confirm.instance_id} busy={stopping} error={closeError} onCancel={() => setConfirm(undefined)} onConfirm={() => void stop()}/>}
    <Toasts items={toasts} onDismiss={dismiss}/>
  </div>;
}
