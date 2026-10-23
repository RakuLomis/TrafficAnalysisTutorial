export interface RoundView {
  id: string
  status: 'RUNNING' | 'ENDED'
}

export interface ParticipantView {
  id: string
  nickname: string
  short_id: string
  color: string
  online: boolean
}

export interface SessionView {
  session_id: string
  a_id: string
  b_id: string
  mode: 'plaintext' | 'encrypted'
  state: 'INVITED' | 'NEGOTIATING' | 'ACTIVE' | 'CLOSED'
  created_at_utc?: string
  expires_at_utc?: string
  closed_reason?: string
}

export interface Welcome {
  v: 1
  type: 'welcome'
  request_id: string
  participant_id: string
  nickname: string
  short_id: string
  color: string
  resume_token: string
  connection_epoch: string
  round: RoundView
  topology_version: number
  participants: ParticipantView[]
  sessions: SessionView[]
}

export interface TopologyChanged {
  v: 1
  type: 'topology.changed'
  round_id: string
  connection_epoch: string
  topology_version: number
  participants: ParticipantView[]
  sessions: SessionView[]
}

export interface AdminWelcome {
  v: 1
  type: 'admin.welcome'
  request_id: string
  connection_epoch: string
  server_version: string
  protocol_version: number
  round: RoundView
  topology_version: number
  participants: ParticipantView[]
  sessions: SessionView[]
}

export interface ObserverRecord {
  session_id?: string
  message_id: string
  sender_id: string
  direction: 'a_to_b' | 'b_to_a'
  mode: 'plaintext' | 'encrypted'
  payload_bytes: number
  ws_message_bytes: number
  at_utc: string
  mono_ms: number
  interval_ms: number | null
  receipt_status: 'pending' | 'confirmed' | 'unconfirmed' | 'failed'
  text: string | null
  nonce_b64: string | null
  ciphertext_b64: string | null
}

export interface ObserverStats {
  message_count: number
  payload_bytes_total: number
  ws_message_bytes_total: number
  last_direction: string
  direction_switches: number
  confirmed_count: number
  unconfirmed_count: number
  failed_count: number
  first_at_utc: string
  last_at_utc: string
}

export interface ObserverSnapshot {
  v: 1
  type: 'observer.snapshot'
  request_id?: string
  session_id: string
  mode: string
  stats: ObserverStats
  truncated: boolean
  messages: ObserverRecord[]
}

export interface ObserverEvent {
  v: 1
  type: 'observer.event'
  session_id: string
  message: ObserverRecord
  update: boolean
  stats: ObserverStats
  truncated: boolean
}

export interface SessionChanged {
  v: 1
  type: 'session.changed'
  round_id: string
  connection_epoch: string
  session: SessionView
}

export interface ChatAccepted {
  v: 1
  type: 'chat.accepted'
  request_id: string
  round_id: string
  connection_epoch: string
  session_id: string
  message_id: string
  seq: string
  accepted_at_utc: string
}

export interface ChatDeliver {
  v: 1
  type: 'chat.deliver'
  round_id: string
  connection_epoch: string
  session_id: string
  message_id: string
  sender_id: string
  seq: string
  text?: string
  nonce_b64?: string
  ciphertext_b64?: string
  sent_at_utc: string
}

export interface ChatStatus {
  v: 1
  type: 'chat.status'
  round_id: string
  connection_epoch: string
  session_id: string
  message_id: string
  status: 'confirmed' | 'unconfirmed' | 'failed'
  at_utc: string
}

export interface ActivityEvent {
  session_id: string
  message_id: string
  sender_id: string
  direction: 'a_to_b' | 'b_to_a'
  at_utc: string
}

export interface ActivityBatch {
  v: 1
  type: 'activity.batch'
  round_id: string
  connection_epoch: string
  events: ActivityEvent[]
}

export interface KeyPublishMessage {
  v: 1
  type: 'key.publish'
  round_id: string
  connection_epoch: string
  session_id: string
  sender_id: string
  public_key_b64: string
}

export interface KeyConfirmMessage {
  v: 1
  type: 'key.confirm'
  round_id: string
  connection_epoch: string
  session_id: string
  sender_id: string
  seq: string
  nonce_b64: string
  ciphertext_b64: string
}

export interface ErrorMessage {
  v: 1
  type: 'error'
  code: string
  message: string
  request_id?: string
  session_id?: string
}

export interface Pong {
  v: 1
  type: 'pong'
  server_time_utc: string
}

export interface RoundEnded {
  v: 1
  type: 'round.ended'
  reason: 'reset' | 'class_end' | 'shutdown'
  new_round_id?: string
}

export type ServerMessage =
  | Welcome
  | AdminWelcome
  | ObserverSnapshot
  | ObserverEvent
  | TopologyChanged
  | SessionChanged
  | ChatAccepted
  | ChatDeliver
  | ChatStatus
  | ActivityBatch
  | KeyPublishMessage
  | KeyConfirmMessage
  | ErrorMessage
  | Pong
  | RoundEnded

export function parseServerMessage(data: string): ServerMessage | null {
  try {
    const value: unknown = JSON.parse(data)
    if (typeof value !== 'object' || value === null) {
      return null
    }
    const record = value as Record<string, unknown>
    if (record.v !== 1 || typeof record.type !== 'string') {
      return null
    }
    return value as ServerMessage
  } catch {
    return null
  }
}

