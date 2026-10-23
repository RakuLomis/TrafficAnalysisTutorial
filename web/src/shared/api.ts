export interface PublicLimits {
  max_nickname_runes: number
  max_body_bytes: number
  max_ciphertext_bytes: number
  max_sessions_per_participant: number
  invite_timeout_ms: number
  key_timeout_ms: number
  ack_timeout_ms: number
  heartbeat_ms: number
  render_interval_ms: number
  client_history_per_session: number
  client_cache_bytes: number
}

export interface IfaceCandidate {
  name: string
  ip: string
  loopback: boolean
  link_local: boolean
  virtual: boolean
  usable: boolean
}

export interface PublicConfig {
  v: number
  protocol_version: number
  server_version?: string
  round: {
    id: string
    status: 'RUNNING' | 'ENDED'
  }
  limits: PublicLimits
  capabilities: {
    plaintext: boolean
    encryption: boolean
    resume: boolean
  }
  student_url?: string
  student_port?: number
  iface_candidates?: IfaceCandidate[]
}

export async function fetchConfig(): Promise<PublicConfig> {
  const response = await fetch('/api/config', {
    cache: 'no-store',
    headers: { Accept: 'application/json' },
  })
  if (!response.ok) {
    throw new Error(`配置请求失败：HTTP ${response.status}`)
  }
  return (await response.json()) as PublicConfig
}

export async function fetchTeacherInfo(): Promise<PublicConfig> {
  const response = await fetch('/api/teacher-info', {
    cache: 'no-store',
    headers: { Accept: 'application/json' },
  })
  if (!response.ok) {
    throw new Error(`教师信息请求失败：HTTP ${response.status}`)
  }
  return (await response.json()) as PublicConfig
}
