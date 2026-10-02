import { expect, test } from 'vitest';
import { applyEvent } from './model';
import { demoMessages } from './demo/client';
test('replay deduplicates messages and preserves delivery before message', () => {
  const m=demoMessages()[0]; const delivery = { v:1 as const, sub:'s', event:'delivery' as const, data:{ instance_id:m.instance_id,event_seq:1,message_id:m.message_id,status:'delivered' } };
  const event={v:1 as const,sub:'s',event:'message' as const,data:{ instance_id:m.instance_id,event_seq:2,message:m }};
  const state=applyEvent(applyEvent(applyEvent({messages:[],statuses:{}},delivery,'orchestrator'),event,'orchestrator'),event,'orchestrator');
  expect(state.messages).toHaveLength(1); expect(state.statuses[m.message_id]).toBe('delivered');
});
