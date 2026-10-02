import { vi } from 'vitest';
vi.stubGlobal('matchMedia',()=>({ matches:false, addEventListener:vi.fn(), removeEventListener:vi.fn() }));

Element.prototype.scrollIntoView=vi.fn();
vi.stubGlobal('ResizeObserver',class {observe(){} unobserve(){} disconnect(){}});
HTMLDialogElement.prototype.showModal=function(){this.setAttribute('open','');};
