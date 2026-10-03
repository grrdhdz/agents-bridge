import type { Event, Message, Role, RoleState, Instance } from './api/types';
export const labels = ['TAREA', 'PREGUNTA', 'RESPUESTA', 'RESULTADO', 'FIN', 'URGENTE', 'PROGRESO'] as const;
export const roleName = (role: Role) => role === 'orchestrator' ? 'Orquestador' : 'Ejecutor';
export const messageRole = (m: Message): Role => m.sender_role === 'mac-orchestrator' ? 'orchestrator' : 'executor';
export const localRole = (i: Instance): Role => i.roles.includes('orchestrator') ? 'orchestrator' : 'executor';
export const roleState = (i: Instance, r: Role): RoleState | undefined => i.role_states[r] || (r === 'orchestrator' ? i.role_states.orq : undefined);
export function bodyParts(body: string) { const [first, ...rest] = body.split('\n'); return labels.some(l => l === first) ? { label: first, body: rest.join('\n') } : { label: 'Mensaje', body }; }
export const symbols: Record<string, string> = { 'queued-ram': '·', accepted: '✓', received: '✓', delivered: '✓✓', rejected: '✗' };
export const statusNames: Record<string, string> = { 'queued-ram': 'Enviando', accepted: 'Aceptado', received: 'Recibido', delivered: 'Entregado', rejected: 'Rechazado' };
// order keeps the journal event_seq of each message: the local role's own messages
// arrive with server_seq 0, so server_seq cannot order a mixed conversation.
export type ChatState = { messages: Message[]; statuses: Record<string, string>; order?: Record<string, number> };
export function applyEvent(state: ChatState, event: Event, role: Role): ChatState {
  if (event.event === 'message') {
    const m = event.data.message;
    const order = { ...state.order, [m.message_id]: Math.min(state.order?.[m.message_id] ?? Infinity, event.data.event_seq) };
    const key = (x: Message) => order[x.message_id] ?? Infinity;
    const messages = [...state.messages.filter(p => p.message_id !== m.message_id), m].sort((a, b) => key(a) === key(b) ? 0 : key(a) - key(b));
    return { order, messages, statuses: { ...state.statuses, [m.message_id]: state.statuses[m.message_id] || event.data.status || (messageRole(m) === role ? 'accepted' : 'received') } };
  }
  if (event.event === 'delivery') return { ...state, statuses: { ...state.statuses, [event.data.message_id]: event.data.status } };
  return state;
}
