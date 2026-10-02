import { invoke } from '@tauri-apps/api/core';
import type { HelloResult } from './types';

// El contrato generado es la única dependencia del motor en la interfaz.
export function hello(): Promise<HelloResult> {
  return invoke<HelloResult>('engine_hello');
}
