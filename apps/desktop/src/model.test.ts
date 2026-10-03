import { expect, test } from 'vitest';
import { applyEvent } from './model';
import { demoMessages } from './demo/client';
test('replay deduplicates messages and preserves delivery before message', () => {
  const m=demoMessages()[0]; const delivery = { v:1 as const, sub:'s', event:'delivery' as const, data:{ instance_id:m.instance_id,event_seq:1,message_id:m.message_id,status:'delivered' } };
  const event={v:1 as const,sub:'s',event:'message' as const,data:{ instance_id:m.instance_id,event_seq:2,message:m }};
  const state=applyEvent(applyEvent(applyEvent({messages:[],statuses:{}},delivery,'orchestrator'),event,'orchestrator'),event,'orchestrator');
  expect(state.messages).toHaveLength(1); expect(state.statuses[m.message_id]).toBe('delivered');
});
test('messages follow bridge order even when local messages carry server_seq 0', () => {
  const [base] = demoMessages();
  const msg = (id: string, role: 'mac-orchestrator' | 'win-executor', server_seq: number) => ({ ...base, message_id: id, sender_role: role, server_seq, body: `TAREA\n${id}` });
  const ev = (event_seq: number, m: ReturnType<typeof msg>) => ({ v: 1 as const, sub: 's', event: 'message' as const, data: { instance_id: base.instance_id, event_seq, message: m } });
  const events = [ev(2, msg('tarea', 'mac-orchestrator', 0)), ev(5, msg('resultado', 'win-executor', 2)), ev(7, msg('pregunta', 'mac-orchestrator', 0)), ev(10, msg('respuesta', 'win-executor', 4)), ev(12, msg('fin', 'mac-orchestrator', 0))];
  let state = { messages: [], statuses: {} } as Parameters<typeof applyEvent>[0];
  for (const e of [...events, events[1]]) state = applyEvent(state, e, 'orchestrator');
  expect(state.messages.map(m => m.message_id)).toEqual(['tarea', 'resultado', 'pregunta', 'respuesta', 'fin']);
});
