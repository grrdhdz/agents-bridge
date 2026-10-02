import type { Message, Role } from '../api/types';
import { bodyParts, messageRole, roleName, symbols, statusNames } from '../model';
export default function MessageCard({ message, status, local }: { message: Message; status: string; local: Role }) {
  const role = messageRole(message); const parts = bodyParts(message.body); const human = message.source === 'human-operator';
  return <article tabIndex={0} className={`message ${role} ${role === local ? 'outgoing' : 'incoming'}`} aria-label={`${roleName(role)}, ${parts.label}, ${statusNames[status] || status}`}>
    <div className="message-meta"><strong className={`role ${role}`}>{roleName(role)}</strong><span className={human ? 'human' : 'origin'}>{human ? 'Humano' : 'Agente'}</span><span className={`label label-${parts.label.toLowerCase()}`}>{parts.label}</span><time dateTime={message.created_at}>{new Date(message.created_at).toLocaleTimeString('es', { hour: '2-digit', minute: '2-digit' })}</time><span className={status === 'rejected' ? 'delivery rejected' : 'delivery'} aria-label={statusNames[status] || status}>{symbols[status] || '·'}</span></div>
    <p className="message-body">{parts.body}</p>
  </article>;
}
