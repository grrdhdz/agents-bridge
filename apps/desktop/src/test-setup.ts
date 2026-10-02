import { vi } from 'vitest';
vi.stubGlobal('matchMedia',()=>({ matches:false, addEventListener:vi.fn(), removeEventListener:vi.fn() }));
