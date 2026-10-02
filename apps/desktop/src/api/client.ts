import { invoke } from '@tauri-apps/api/core';
import { getCurrentWebviewWindow } from '@tauri-apps/api/webviewWindow';
import type { HelloResult, ListResult, SubscribeArgs, SubscribeResult, UnsubscribeArgs, UnsubscribeResult, SendArgs, SendResult, StopArgs, StopResult, CreateLocalArgs, CreateLocalResult, HealthArgs, Health, ExportArgs, ExportResult, Event, IntegrationStatusResult, IntegrationEnsureResult, IntegrationSetArgs } from './types';

export type EngineStatus = { status: 'connected' | 'restarting' | 'subscription-error'; message?: string };
export type Off = () => void;
export interface ApiClient {
  hello(): Promise<HelloResult>;
  integrationStatus(): Promise<IntegrationStatusResult>;
  integrationEnsure(): Promise<IntegrationEnsureResult>;
  integrationSet(args: IntegrationSetArgs): Promise<IntegrationEnsureResult>;
  list(): Promise<ListResult>;
  subscribe(args: SubscribeArgs): Promise<SubscribeResult>;
  unsubscribe(args: UnsubscribeArgs): Promise<UnsubscribeResult>;
  send(args: SendArgs): Promise<SendResult>;
  stop(args: StopArgs): Promise<StopResult>;
  createLocal(args: CreateLocalArgs): Promise<CreateLocalResult>;
  health(args: HealthArgs): Promise<Health>;
  export(args: ExportArgs): Promise<ExportResult>;
  onEvent(fn: (event: Event) => void): Promise<Off>;
  onStatus(fn: (status: EngineStatus) => void): Promise<Off>;
}
const call = <T,>(op: string, args: object = {}): Promise<T> => invoke<T>(`engine_${op}`, { args });
export const realClient: ApiClient = {
  integrationStatus: () => call('integration_status'), integrationEnsure: () => call('integration_ensure'), integrationSet: args => call('integration_set', args),
  hello: () => call('hello'), list: () => call('list'),
  subscribe: args => call('subscribe', args), unsubscribe: args => call('unsubscribe', args),
  send: args => call('send', args), stop: args => call('stop', args),
  createLocal: args => call('create_local', args), health: args => call('health', args), export: args => call('export', args),
  onEvent: async fn => getCurrentWebviewWindow().listen<Event>('agents-bridge-event', event => fn(event.payload)),
  onStatus: async fn => getCurrentWebviewWindow().listen<EngineStatus>('agents-bridge-status', event => fn(event.payload)),
};
export const hello = realClient.hello;
