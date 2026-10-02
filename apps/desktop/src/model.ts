import type { Event, Message, Role, RoleState, Instance } from './api/types';
export const labels = ['TAREA', 'PREGUNTA', 'RESPUESTA', 'RESULTADO', 'FIN', 'URGENTE', 'PROGRESO'] as const;
export const roleName = (role: Role) => role === 'orchestrator' ? 'Orquestador' : 'Ejecutor';
export const messageRole = (m: Message): Role => m.sender_role === 'mac-orchestrator' ? 'orchestrator' : 'executor';
export const localRole = (i: Instance): Role => i.roles.includes('orchestrator') ? 'orchestrator' : 'executor';
export const roleState = (i: Instance, r: Role): RoleState | undefined => i.role_states[r] || (r === 'orchestrator' ? i.role_states.orq : undefined);
export function bodyParts(body: string) { const [first, ...rest] = body.split('\n'); return labels.some(l => l === first) ? { label: first, body: rest.join('\n') } : { label: 'Mensaje', body }; }
export const symbols: Record<string, string> = { 'queued-ram': '·', accepted: '✓', received: '✓', delivered: '✓✓', rejected: '✗' };
export const statusNames: Record<string, string> = { 'queued-ram': 'Enviando', accepted: 'Aceptado', received: 'Recibido', delivered: 'Entregado', rejected: 'Rechazado' };
export type ChatState = { messages: Message[]; statuses: Record<string, string> };
export function applyEvent(state: ChatState, event: Event, role: Role): ChatState {
  if (event.event === 'message') {
    const m = event.data.message;
    const messages = [...state.messages.filter(p => p.message_id !== m.message_id), m].sort((a, b) => (a.server_seq || Infinity) - (b.server_seq || Infinity));
    return { messages, statuses: { ...state.statuses, [m.message_id]: state.statuses[m.message_id] || event.data.status || (messageRole(m) === role ? 'accepted' : 'received') } };
  }
  if (event.event === 'delivery') return { ...state, statuses: { ...state.statuses, [event.data.message_id]: event.data.status } };
  return state;
}
