import { afterEach, expect, test } from 'vitest';
import { cleanup,render,screen } from '@testing-library/react';
import Sidebar from './Sidebar';
import { demoInstances } from '../demo/client';
afterEach(cleanup);
test('participant hooks heartbeat unread count and totals are visible',()=>{const i=demoInstances()[0];render(<Sidebar instance={i} health={{instance_id:i.instance_id,state:'running',pid:i.pid,peer_connected:true,fin_received:false,latest_server_seq:7,role_states:i.role_states,unread:2}} total={7} sent={4} pending={1} local="orchestrator"/>);expect(screen.getAllByText('✓ Hook vinculado')).toHaveLength(2);expect(screen.getByText('Sin leer (agente local)')).toBeDefined();expect(screen.getByText('2')).toBeDefined();expect(screen.getByText('Bash')).toBeDefined();});
