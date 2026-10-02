import { afterEach, expect, test } from 'vitest';
import { cleanup,render,screen } from '@testing-library/react';
import MessageCard from './MessageCard';
import { demoMessages } from '../demo/client';
afterEach(cleanup);
test('role border, side, human origin, label and delivery are exposed',()=>{const m=demoMessages()[4];render(<MessageCard message={m} status="rejected" local="orchestrator"/>);expect(screen.getByRole('article').className).toContain('outgoing');expect(screen.getByText('Humano')).toBeDefined();expect(screen.getByText('URGENTE')).toBeDefined();expect(screen.getByLabelText('Rechazado').textContent).toBe('✗');});
