import { expect, test, vi } from 'vitest';
import { exportConversation } from './export';
import { createDemoClient } from './demo/client';
test('export uses selected native save path and public API only', async () => {
  const client=createDemoClient(), call=vi.spyOn(client,'export'), save=vi.fn().mockResolvedValue('/tmp/conversation.md');
  expect(await exportConversation(client,'instance','md',save)).toBe('/tmp/conversation.md');
  expect(call).toHaveBeenCalledWith({instance_id:'instance',format:'md',output:'/tmp/conversation.md'});
  expect(save.mock.calls[0][0].filters[0].extensions).toEqual(['md']);
});
test('canceling the save dialog does not export', async () => {
  const client=createDemoClient(), call=vi.spyOn(client,'export');
  expect(await exportConversation(client,'instance','jsonl',vi.fn().mockResolvedValue(null))).toBeNull();
  expect(call).not.toHaveBeenCalled();
});
