import type { ApiClient, EngineStatus } from '../api/client';
import type { Instance, Message, Event, Health, Label, IntegrationStatusResult } from '../api/types';

const marker = 'DEMO_DATA_ONLY';
const beat = () => new Date(Date.now() - 8000).toISOString();
export function demoInstances(): Instance[] {
  return [
    { instance_id: 'demo-checkout-001', project: 'checkout-api', mode: 'local', roles: ['orchestrator', 'executor'], pid: 4812, started_at: beat(), idle_seconds: 3, peer_connected: true, latest_server_seq: 7, role_states: { orchestrator: { state: 'esperando', hook_bound: true, last_heartbeat_at: beat() }, executor: { state: 'trabajando', tool: 'Bash', hook_bound: true, last_heartbeat_at: beat() } } },
    { instance_id: 'demo-docs-002', project: 'docs-site', mode: 'local', roles: ['orchestrator', 'executor'], pid: 4910, started_at: beat(), idle_seconds: 18, peer_connected: true, latest_server_seq: 12, role_states: { orchestrator: { state: 'trabajando', tool: 'Read', hook_bound: true, last_heartbeat_at: beat() }, executor: { state: 'esperando', hook_bound: true, last_heartbeat_at: beat() } } },
    { instance_id: 'demo-desktop-003', project: 'desktop-client', mode: 'host', roles: ['orchestrator'], pid: 5201, started_at: beat(), idle_seconds: 42, peer_connected: false, latest_server_seq: 4, role_states: { orchestrator: { state: 'callado', hook_bound: false }, executor: { state: '—', hook_bound: false } } },
  ];
}
export function demoMessages(id = 'demo-checkout-001'): Message[] {
  const rows: [Label, string, boolean, boolean][] = [
    ['TAREA', 'Revisa el proxy y entrega las pruebas de integración.', true, false],
    ['PREGUNTA', '¿Mantenemos el replay completo al reconectar?', false, false],
    ['RESPUESTA', 'Sí. Conserva el historial y evita mensajes duplicados.', true, false],
    ['PROGRESO', 'Correlación y timeouts listos. Comprobando el reinicio.', false, false],
    ['URGENTE', 'Incluye la prueba de cierre sin detener los puentes.', true, true],
    ['RESULTADO', 'Pruebas en verde. **Proxy recuperado** y suscripción restaurada.\n\n```go\nfunc main() {\n    status := "conectado"\n    fmt.Println(status)\n}\n```', false, false],
    ['FIN', 'Validación completa. Puedes cerrar esta tarea.', true, false],
  ];
  return rows.map(([label, body, orq, human], n) => ({ protocol_version: 1, instance_id: id, message_id: `demo-message-${n}`, client_seq: n + 1, server_seq: n + 1, sender_id: orq ? 'claude' : 'codex', sender_role: orq ? 'mac-orchestrator' : 'win-executor', kind: 'text', body: `${label}\n${body}`, body_sha256: '0'.repeat(64), source: human ? 'human-operator' : 'agent-control', created_at: `2026-10-02T00:0${n}:00-06:00`, accepted_at: `2026-10-02T00:0${n}:00-06:00` }));
}
export function createDemoClient(): ApiClient {
  let instances = demoInstances(); let counter = 0; const history = new Map(instances.map(i => [i.instance_id, demoMessages(i.instance_id)]));
  const events = new Set<(e: Event) => void>(); const status = new Set<(s: EngineStatus) => void>(); const subs = new Map<string, string>();
  const health = (id: string): Health => { const i = instances.find(i => i.instance_id === id); if (!i) throw new Error('Puente cerrado'); return { instance_id: id, state: 'running', pid: i.pid, peer_connected: i.peer_connected, fin_received: false, latest_server_seq: i.latest_server_seq, role_states: i.role_states, unread: id.startsWith('demo-new') ? 0 : 2 }; };
  let integration: IntegrationStatusResult = {
    cli_path: '/Users/demo/.local/bin/agents-bridge', cli_on_path: true, cli_current: true,
    harnesses: {
      claude: { installed: true, opted_out: false, path: '/Users/demo/.claude/settings.json' },
      codex: { installed: true, opted_out: false, path: '/Users/demo/.codex/hooks.json', trust_note: 'Codex: acepta los hooks como confiables y marca cada proyecto como trusted.' },
    },
  };
  return {
    integrationStatus: async () => integration,
    integrationEnsure: async () => ({ ...integration, changed: false }),
    integrationSet: async ({ harness, enabled }) => {
      integration = { ...integration, harnesses: { ...integration.harnesses, [harness]: { ...integration.harnesses[harness], installed: enabled, opted_out: !enabled } } };
      return { ...integration, changed: true };
    },
    hello: async () => { void marker; return { engine_version: 'v0.5.2-demo', contract_version: 1 }; },
    list: async () => ({ instances: [...instances] }), health: async a => health(a.instance_id),
    subscribe: async a => { const sub = `demo-sub-${++counter}`; subs.set(sub, a.instance_id); (history.get(a.instance_id) || []).forEach((message, n) => { events.forEach(fn => fn({ v: 1, sub, event: 'message', data: { instance_id: a.instance_id, event_seq: n * 2 + 1, message, status: ['delivered', 'received', 'accepted', 'received', 'rejected', 'delivered', 'queued-ram'][n] } })); }); return { sub }; },
    unsubscribe: async a => { subs.delete(a.sub); return { unsubscribed: a.sub }; },
    send: async a => { const id = `demo-sent-${++counter}`; const i = instances.find(i => i.instance_id === a.instance_id); if (!i) throw new Error('Puente cerrado'); i.latest_server_seq += 1; const message = { ...demoMessages(a.instance_id)[0], message_id: id, server_seq: i.latest_server_seq, source: 'human-operator', body: `${a.label || 'RESPUESTA'}\n${a.body}`, created_at: new Date().toISOString(), accepted_at: new Date().toISOString() }; history.set(a.instance_id, [...(history.get(a.instance_id) || []), message]); subs.forEach((instance, sub) => { if (instance === a.instance_id) events.forEach(fn => fn({ v: 1, sub, event: 'message', data: { instance_id: instance, event_seq: counter + 20, message, status: 'accepted' } })); }); return { instance_id: a.instance_id, message_id: id, role: 'orchestrator', source: 'human-operator' }; },
    stop: async a => { instances = instances.filter(i => i.instance_id !== a.instance_id); return { instance_id: a.instance_id, state: 'stopping' }; },
    createLocal: async () => { const id = `demo-new-${++counter}`; instances = [{ ...demoInstances()[0], instance_id: id, project: 'nuevo-puente', latest_server_seq: 0, role_states: { orchestrator: { state: '—', hook_bound: false }, executor: { state: '—', hook_bound: false } } }, ...instances]; history.set(id, []); return { instance_id: id, state: 'running' }; },
    export: async a => a,
    onEvent: async fn => { events.add(fn); return () => { events.delete(fn); }; },
    onStatus: async fn => { status.add(fn); return () => { status.delete(fn); }; },
  };
}

export function demoPreviews(){return Object.fromEntries(demoInstances().map(i=>[i.instance_id,i.latest_server_seq?demoMessages(i.instance_id).slice(-4).map(m=>({role:m.sender_role==='mac-orchestrator'?'orchestrator' as const:'executor' as const,human:m.source==='human-operator',label:m.body.split('\n')[0]})):[]]));}
