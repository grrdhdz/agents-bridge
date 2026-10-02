import { relativeTime } from '../format';
import type { Health, Instance, Role } from '../api/types';
import { roleName, roleState } from '../model';
export default function Sidebar({ instance, health, total, sent, pending, local }: { instance: Instance; health?: Health; total: number; sent: number; pending: number; local: Role }) {
  const merged = { ...instance, role_states: health?.role_states || instance.role_states };
  return <aside className="sidebar" aria-label="Estado del puente"><h2>Participantes</h2>{(['orchestrator', 'executor'] as const).map(r => { const s = roleState(merged, r); return <section className="participant" key={r}><div className={`role ${r}`}><strong>{roleName(r)}</strong><span>{r === local ? 'Local' : 'Otro agente'}</span></div><p className="participant-state">{s?.state || '—'}{s?.tool && <span>{s.tool}</span>}</p><p>{s?.hook_bound ? '✓ Hook vinculado' : '○ Sin hook vinculado'}</p><p>Latido <time dateTime={s?.last_heartbeat_at}>{s?.last_heartbeat_at ? relativeTime(s.last_heartbeat_at) : '—'}</time></p></section>; })}
    <h2>Actividad</h2><dl className="metrics"><div><dt>Mensajes totales</dt><dd>{total}</dd></div><div><dt>Rol local</dt><dd>{sent}</dd></div><div><dt>Otro rol</dt><dd>{Math.max(0, total - sent)}</dd></div><div><dt>Enviados sin entregar</dt><dd>{pending}</dd></div><div><dt>Pendientes de leer por {roleName(local)}</dt><dd>{health?.unread ?? '—'}</dd></div></dl>
    <div className="sidebar-foot"><p>◉ {health?.peer_connected ?? instance.peer_connected ? 'Compañero conectado' : 'Sin compañero'}</p><p>{health?.fin_received ? 'FIN recibido por el agente' : 'Conversación en curso'}</p><p>Modo {instance.mode} · PID {instance.pid}</p></div>
  </aside>;
}
