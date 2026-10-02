/* Generado desde engine/api/schema.json. No editar.
 * SHA256: 36f8b6398aa805894e79e0614e2dfe4cd08b40efb25dd0ec3fa4be2006d416e4
 * npm run generate:types
 */

export type AgentsBridgePacket = Request | Success | Failure | Event;
export type Request =
  | HelloRequest
  | ListRequest
  | SubscribeRequest
  | UnsubscribeRequest
  | SendRequest
  | StopRequest
  | CreateLocalRequest
  | HealthRequest
  | ExportRequest;
export type Label = 'TAREA' | 'PREGUNTA' | 'RESPUESTA' | 'RESULTADO' | 'FIN' | 'URGENTE' | 'PROGRESO';
export type Role = 'orchestrator' | 'executor';
export type Event = MessageEvent | DeliveryEvent | StateEvent | TransportEvent | LifecycleEvent;

export interface HelloRequest {
  v: 1;
  id: string;
  op: 'hello';
  args: HelloArgs;
}
export interface HelloArgs {}
export interface ListRequest {
  v: 1;
  id: string;
  op: 'list';
  args: ListArgs;
}
export interface ListArgs {}
export interface SubscribeRequest {
  v: 1;
  id: string;
  op: 'subscribe';
  args: SubscribeArgs;
}
export interface SubscribeArgs {
  instance_id: string;
}
export interface UnsubscribeRequest {
  v: 1;
  id: string;
  op: 'unsubscribe';
  args: UnsubscribeArgs;
}
export interface UnsubscribeArgs {
  sub: string;
}
export interface SendRequest {
  v: 1;
  id: string;
  op: 'send';
  args: SendArgs;
}
export interface SendArgs {
  instance_id: string;
  body: string;
  label?: Label;
}
export interface StopRequest {
  v: 1;
  id: string;
  op: 'stop';
  args: StopArgs;
}
export interface StopArgs {
  instance_id: string;
}
export interface CreateLocalRequest {
  v: 1;
  id: string;
  op: 'create_local';
  args: CreateLocalArgs;
}
export interface CreateLocalArgs {
  idle_timeout?: string;
}
export interface HealthRequest {
  v: 1;
  id: string;
  op: 'health';
  args: HealthArgs;
}
export interface HealthArgs {
  instance_id: string;
}
export interface ExportRequest {
  v: 1;
  id: string;
  op: 'export';
  args: ExportArgs;
}
export interface ExportArgs {
  instance_id: string;
  format: 'md' | 'jsonl';
  output: string;
}
export interface Success {
  v: 1;
  id: string;
  ok: true;
  result:
    | HelloResult
    | ListResult
    | SubscribeResult
    | UnsubscribeResult
    | SendResult
    | StopResult
    | CreateLocalResult
    | ExportResult
    | Health;
}
export interface HelloResult {
  engine_version: string;
  contract_version: 1;
}
export interface ListResult {
  instances: Instance[];
}
export interface Instance {
  instance_id: string;
  mode: string;
  roles: Role[];
  pid: number;
  started_at: string;
  project: string;
  idle_seconds?: number;
  peer_connected: boolean;
  latest_server_seq: number;
  role_states: RoleStates;
}
export interface RoleStates {
  [k: string]: RoleState;
}
export interface RoleState {
  state: 'esperando' | 'trabajando' | 'callado' | '—';
  hook_bound: boolean;
  tool?: string;
  last_heartbeat_at?: string;
  last_message_at?: string;
  last_wait_at?: string;
  last_message_age_seconds?: number;
}
export interface SubscribeResult {
  sub: string;
}
export interface UnsubscribeResult {
  unsubscribed: string;
}
export interface SendResult {
  instance_id: string;
  message_id: string;
  source: 'human-operator';
  role: Role;
}
export interface StopResult {
  instance_id: string;
  state: 'stopping';
}
export interface CreateLocalResult {
  instance_id: string;
  state: 'running';
}
export interface ExportResult {
  instance_id: string;
  format: 'md' | 'jsonl';
  output: string;
}
export interface Health {
  instance_id: string;
  state: string;
  pid: number;
  peer_connected: boolean;
  fin_received: boolean;
  latest_server_seq: number;
  role_states: RoleStates;
  unread?: number;
}
export interface Failure {
  v: 1;
  id: string;
  ok: false;
  error: {
    code: string;
    message: string;
  };
}
export interface MessageEvent {
  v: 1;
  sub: string;
  event: 'message';
  data: {
    instance_id: string;
    event_seq: number;
    server_seq?: number;
    message_id?: string;
    status?: string;
    state?: string;
    detail?: string;
    message: Message;
    health?: Health;
  };
}
export interface Message {
  protocol_version: 1;
  instance_id: string;
  message_id: string;
  client_seq: number;
  server_seq: number;
  sender_id: string;
  sender_role: 'mac-orchestrator' | 'win-executor';
  kind: string;
  body: string;
  body_sha256: string;
  source: string;
  created_at: string;
  accepted_at: string;
}
export interface DeliveryEvent {
  v: 1;
  sub: string;
  event: 'delivery';
  data: {
    instance_id: string;
    event_seq: number;
    server_seq?: number;
    message_id: string;
    status: string;
    state?: string;
    detail?: string;
    message?: Message;
    health?: Health;
  };
}
export interface StateEvent {
  v: 1;
  sub: string;
  event: 'state';
  data: {
    instance_id: string;
    event_seq?: number;
    server_seq?: number;
    message_id?: string;
    status?: string;
    state?: string;
    detail?: string;
    message?: Message;
    health?: Health;
  };
}
export interface TransportEvent {
  v: 1;
  sub: string;
  event: 'transport';
  data: {
    instance_id: string;
    event_seq?: number;
    server_seq?: number;
    message_id?: string;
    status?: string;
    state?: string;
    detail?: string;
    message?: Message;
    health?: Health;
  };
}
export interface LifecycleEvent {
  v: 1;
  sub: string;
  event: 'lifecycle';
  data: {
    instance_id: string;
    event_seq?: number;
    server_seq?: number;
    message_id?: string;
    status?: string;
    state?: string;
    detail?: string;
    message?: Message;
    health?: Health;
  };
}
