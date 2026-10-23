import { fetchTeacherInfo, fetchConfig, type PublicConfig } from '../shared/api'
import { ClassroomConnection } from '../shared/connection'
import { GraphView } from '../graph/graph'
import { qrDataURL } from '../shared/qr'
import { buildStudentURL, isDisplayableHost, portFromAddress } from '../shared/url'
import { randomHex } from '../shared/random'
import type {
  AdminWelcome,
  ObserverEvent,
  ObserverRecord,
  ObserverSnapshot,
  ObserverStats,
  ParticipantView,
  RoundView,
  ServerMessage,
  SessionView,
  TopologyChanged,
} from '../shared/protocol'

function byId<T extends HTMLElement>(id: string): T {
  const element = document.getElementById(id)
  if (!element) {
    throw new Error(`missing element: ${id}`)
  }
  return element as T
}

const connectionStatus = byId<HTMLElement>('connection-status')
const teacherConfigStatus = byId<HTMLParagraphElement>('teacher-config-status')
const studentURL = byId<HTMLElement>('student-url')
const copyButton = byId<HTMLButtonElement>('copy-button')
const qrImage = byId<HTMLImageElement>('qr-image')
const qrPlaceholder = byId<HTMLParagraphElement>('qr-placeholder')
const ifaceSelect = byId<HTMLSelectElement>('iface-select')
const ifaceNote = byId<HTMLParagraphElement>('iface-note')
const protocolVersion = byId<HTMLElement>('protocol-version')
const roundStatus = byId<HTMLElement>('round-status')
const roundID = byId<HTMLElement>('round-id')
const teacherWSStatus = byId<HTMLElement>('teacher-ws-status')
const participantCount = byId<HTMLElement>('participant-count')
const topologySvg = document.getElementById('topology') as unknown as SVGSVGElement
const topologyEmpty = byId<HTMLParagraphElement>('topology-empty')
const observerStatus = byId<HTMLParagraphElement>('observer-status')
const pauseButton = byId<HTMLButtonElement>('pause-button')
const sessionSelect = byId<HTMLSelectElement>('session-select')
const observerStats = byId<HTMLDivElement>('observer-stats')
const observerTruncated = byId<HTMLParagraphElement>('observer-truncated')
const timeline = byId<HTMLDivElement>('timeline')
const observerMessages = byId<HTMLDivElement>('observer-messages')
const toast = byId<HTMLDivElement>('toast')

const params = new URLSearchParams(location.hash.replace(/^#/, ''))
const adminToken = params.get('token') ?? ''
if (location.hash) {
  history.replaceState(null, '', location.pathname + location.search)
}

let config: PublicConfig | null = null
let connection: ClassroomConnection | null = null
let connectionEpoch = ''
let round: RoundView | null = null
let participants: ParticipantView[] = []
let sessions: SessionView[] = []
let selectedSessionID: string | null = null
let snapshot: ObserverSnapshot | null = null
let paused = false
let currentURL = ''
let toastTimer = 0

const graph = new GraphView(topologySvg, {
  onSelectSession: (sessionID) => selectSession(sessionID),
  onSelectPeer: () => undefined,
}, { allowAllEdges: true })

function showToast(text: string): void {
  toast.textContent = text
  toast.hidden = false
  window.clearTimeout(toastTimer)
  toastTimer = window.setTimeout(() => {
    toast.hidden = true
  }, 4200)
}

function setConnectionStatus(text: string, error = false): void {
  connectionStatus.textContent = text
  connectionStatus.classList.toggle('error', error)
}

async function loadInfo(): Promise<void> {
  let info: PublicConfig
  try {
    info = await fetchTeacherInfo()
  } catch {
    try {
      info = await fetchConfig()
    } catch (error) {
      teacherConfigStatus.textContent = error instanceof Error ? error.message : '无法读取服务配置'
      teacherConfigStatus.classList.add('error')
      return
    }
  }
  config = info
  teacherConfigStatus.textContent = `服务已连接 · ${info.server_version ?? '开发版'}`
  protocolVersion.textContent = String(info.protocol_version)
  round = info.round
  renderRound()
  const host = info.student_url ? hostFromURL(info.student_url) : ''
  try {
    await applyURL(info.student_url ?? buildStudentURL(host || '0.0.0.0', portFor(info)))
  } catch {
    teacherConfigStatus.textContent = '学生入口地址不可用，请查看程序控制台。'
  }
  fillCandidates(info, host)
}

function renderRound(): void {
  if (!round) {
    return
  }
  roundStatus.textContent = round.status === 'RUNNING' ? '运行中' : '已结束'
  roundID.textContent = round.id
}

function hostFromURL(url: string): string {
  try {
    return new URL(url).hostname
  } catch {
    return ''
  }
}

function portFor(info: PublicConfig): number {
  if (info.student_port && info.student_port > 0) {
    return info.student_port
  }
  if (info.student_url) {
    const port = portFromAddress(new URL(info.student_url).host)
    if (port) {
      return port
    }
  }
  return 8080
}

async function applyURL(url: string): Promise<void> {
  currentURL = url
  studentURL.textContent = url
  copyButton.disabled = false
  qrPlaceholder.hidden = false
  qrPlaceholder.textContent = '二维码生成中…'
  try {
    qrImage.src = await qrDataURL(url)
    qrImage.hidden = false
    qrPlaceholder.hidden = true
  } catch {
    qrImage.hidden = true
    qrPlaceholder.hidden = false
    qrPlaceholder.textContent = '二维码生成失败'
  }
}

function fillCandidates(info: PublicConfig, currentHost: string): void {
  const candidates = (info.iface_candidates ?? []).filter((item) => item.usable && isDisplayableHost(item.ip))
  if (candidates.length === 0) {
    ifaceSelect.innerHTML = '<option value="">未发现可用的局域网地址</option>'
    ifaceSelect.disabled = true
    ifaceNote.textContent = '请用 --iface-ip 指定地址后重启。'
    return
  }
  ifaceSelect.innerHTML = ''
  for (const item of candidates) {
    const option = document.createElement('option')
    option.value = item.ip
    option.textContent = `${item.ip} · ${item.name}${item.virtual ? '（虚拟）' : ''}`
    option.selected = item.ip === currentHost
    ifaceSelect.append(option)
  }
  ifaceSelect.disabled = false
  ifaceNote.textContent = '切换只改变本页二维码；持久选择请使用 --iface-ip 重启。'
  ifaceSelect.addEventListener('change', () => {
    if (!config) {
      return
    }
    try {
      void applyURL(buildStudentURL(ifaceSelect.value, portFor(config)))
    } catch {
      showToast('地址无效')
    }
  })
}

async function copyURL(): Promise<void> {
  if (!currentURL) {
    return
  }
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(currentURL)
    } else {
      const area = document.createElement('textarea')
      area.value = currentURL
      area.style.position = 'fixed'
      area.style.opacity = '0'
      document.body.append(area)
      area.select()
      document.execCommand('copy')
      area.remove()
    }
    copyButton.textContent = '已复制'
    window.setTimeout(() => {
      copyButton.textContent = '复制'
    }, 1500)
  } catch {
    copyButton.textContent = '复制失败'
  }
}

function connectAdmin(): void {
  if (!adminToken) {
    teacherWSStatus.textContent = '缺少管理 token'
    teacherWSStatus.classList.add('error')
    setConnectionStatus('未认证', true)
    return
  }
  connection = new ClassroomConnection({
    onOpen: () => {
      connection?.send({
        v: 1,
        type: 'admin.auth',
        request_id: randomHex(),
        token: adminToken,
      })
    },
    onMessage: handleServerMessage,
    onClose: () => {
      teacherWSStatus.textContent = '连接已断开'
      setConnectionStatus('未连接', true)
      pauseButton.disabled = true
    },
    onError: () => showToast('教师 WebSocket 连接错误'),
  }, 15000, '/ws/admin')
  connection.connect()
}

function handleServerMessage(message: ServerMessage): void {
  switch (message.type) {
    case 'admin.welcome':
      handleAdminWelcome(message)
      break
    case 'topology.changed':
      handleTopology(message)
      break
    case 'observer.snapshot':
      handleSnapshot(message)
      break
    case 'observer.event':
      handleObserverEvent(message)
      break
    case 'error':
      showToast(`${message.code}: ${message.message}`)
      if (message.code === 'UNAUTHORIZED' || message.code === 'FORBIDDEN') {
        teacherWSStatus.textContent = '鉴权失败'
        teacherWSStatus.classList.add('error')
        setConnectionStatus('未认证', true)
      }
      break
    case 'pong':
      break
    default:
      break
  }
}

function handleAdminWelcome(message: AdminWelcome): void {
  connectionEpoch = message.connection_epoch
  round = message.round
  participants = message.participants
  sessions = message.sessions
  teacherWSStatus.textContent = '已认证'
  teacherWSStatus.classList.remove('error')
  setConnectionStatus('已连接')
  renderRound()
  renderTopology()
}

function handleTopology(message: TopologyChanged): void {
  participants = message.participants
  sessions = message.sessions
  if (round) {
    round.id = message.round_id
  }
  renderTopology()
}

function handleSnapshot(message: ObserverSnapshot): void {
  if (message.session_id !== selectedSessionID) {
    return
  }
  snapshot = message
  renderObservation()
}

function handleObserverEvent(message: ObserverEvent): void {
  if (paused || message.session_id !== selectedSessionID) {
    return
  }
  if (!snapshot || snapshot.session_id !== message.session_id) {
    return
  }
  if (message.update) {
    const index = snapshot.messages.findIndex((record) => record.message_id === message.message.message_id)
    if (index >= 0) {
      snapshot.messages[index] = message.message
    }
  } else {
    snapshot.messages.push(message.message)
  }
  snapshot.stats = message.stats
  snapshot.truncated = message.truncated
  renderObservation()
}

function selectSession(sessionID: string): void {
  if (!connection || !connectionEpoch) {
    return
  }
  selectedSessionID = sessionID
  snapshot = null
  paused = false
  pauseButton.textContent = '暂停观察'
  pauseButton.disabled = false
  sessionSelect.value = sessionID
  observerStatus.textContent = '正在读取观察记录…'
  connection.send({
    v: 1,
    type: 'observer.subscribe',
    request_id: randomHex(),
    session_id: sessionID,
  })
  renderTopology()
  renderObservation()
}

function resubscribe(): void {
  if (!connection || !selectedSessionID) {
    return
  }
  connection.send({
    v: 1,
    type: 'observer.subscribe',
    request_id: randomHex(),
    session_id: selectedSessionID,
  })
}

function renderTopology(): void {
  graph.render(participants, sessions, '', selectedSessionID)
  topologyEmpty.hidden = participants.length > 0
  participantCount.textContent = `${participants.length} 人在线`

  const previous = selectedSessionID ?? ''
  sessionSelect.innerHTML = ''
  if (sessions.length === 0) {
    const option = document.createElement('option')
    option.value = ''
    option.textContent = '尚无会话'
    sessionSelect.append(option)
    sessionSelect.disabled = true
  } else {
    for (const session of sessions) {
      const a = participants.find((item) => item.id === session.a_id)
      const b = participants.find((item) => item.id === session.b_id)
      const option = document.createElement('option')
      option.value = session.session_id
      option.textContent = `${a?.nickname ?? '?'} ↔ ${b?.nickname ?? '?'} · ${session.mode === 'encrypted' ? '加密' : '明文'} · ${stateLabel(session.state)}`
      sessionSelect.append(option)
    }
    sessionSelect.disabled = false
    if (previous && sessions.some((session) => session.session_id === previous)) {
      sessionSelect.value = previous
    }
  }
}

function renderObservation(): void {
  if (!selectedSessionID) {
    observerStatus.textContent = '选择一条会话边开始观察。'
    observerStats.replaceChildren()
    timeline.replaceChildren()
    observerMessages.replaceChildren()
    observerTruncated.hidden = true
    return
  }
  const session = sessions.find((item) => item.session_id === selectedSessionID)
  const a = session ? participants.find((item) => item.id === session.a_id) : undefined
  const b = session ? participants.find((item) => item.id === session.b_id) : undefined
  observerStatus.textContent = session
    ? `${a?.nickname ?? '?'} ↔ ${b?.nickname ?? '?'} · ${session.mode === 'encrypted' ? '加密' : '明文'} · ${stateLabel(session.state)}`
    : '会话已不在当前拓扑中。'
  if (!snapshot) {
    observerStats.replaceChildren()
    timeline.replaceChildren()
    observerMessages.replaceChildren()
    observerTruncated.hidden = true
    return
  }
  observerTruncated.hidden = !snapshot.truncated
  renderStats(snapshot.stats)
  renderTimeline(snapshot.messages)
  renderMessages(snapshot.messages)
}

function renderStats(stats: ObserverStats): void {
  observerStats.replaceChildren()
  const cards: Array<[string, string]> = [
    ['消息数', String(stats.message_count)],
    ['payload 总字节', String(stats.payload_bytes_total)],
    ['WebSocket 总字节', String(stats.ws_message_bytes_total)],
    ['方向切换', String(stats.direction_switches)],
    ['最后方向', stats.last_direction || '—'],
    ['已确认 / 未确认 / 失败', `${stats.confirmed_count} / ${stats.unconfirmed_count} / ${stats.failed_count}`],
  ]
  for (const [label, value] of cards) {
    const card = document.createElement('div')
    card.className = 'stat-card'
    const name = document.createElement('span')
    name.textContent = label
    const number = document.createElement('strong')
    number.textContent = value
    card.append(name, number)
    observerStats.append(card)
  }
}

function renderTimeline(records: ObserverRecord[]): void {
  timeline.replaceChildren()
  if (records.length === 0) {
    const empty = document.createElement('p')
    empty.className = 'muted small'
    empty.textContent = '当前窗口没有业务消息。'
    timeline.append(empty)
    return
  }
  const max = Math.max(...records.map((record) => record.payload_bytes), 1)
  for (const record of records) {
    const row = document.createElement('div')
    row.className = 'timeline-row'
    const label = document.createElement('span')
    label.className = 'timeline-label'
    label.textContent = timeLabel(record.at_utc)
    const track = document.createElement('div')
    track.className = 'timeline-track'
    const bar = document.createElement('div')
    bar.className = `timeline-bar ${record.direction === 'a_to_b' ? 'direction-a' : 'direction-b'}`
    bar.style.width = `${Math.max(2, (record.payload_bytes / max) * 100)}%`
    bar.title = `${record.payload_bytes} B / ws ${record.ws_message_bytes} B`
    track.append(bar)
    const size = document.createElement('span')
    size.className = 'timeline-size'
    size.textContent = `${record.payload_bytes}B${record.interval_ms === null ? '' : ` · Δ${record.interval_ms}ms`}`
    row.append(label, track, size)
    timeline.append(row)
  }
}

function renderMessages(records: ObserverRecord[]): void {
  observerMessages.replaceChildren()
  if (records.length === 0) {
    const empty = document.createElement('p')
    empty.className = 'muted small'
    empty.textContent = '尚无观察记录。'
    observerMessages.append(empty)
    return
  }
  for (const record of records) {
    const card = document.createElement('div')
    card.className = 'observer-record'
    const head = document.createElement('div')
    head.className = 'observer-record-head'
    const direction = record.direction === 'a_to_b' ? 'A → B' : 'B → A'
    head.textContent = `${timeLabel(record.at_utc)} · ${direction} · ${record.receipt_status}`
    const meta = document.createElement('div')
    meta.className = 'observer-record-meta'
    const interval = record.interval_ms === null ? '首条' : `${record.interval_ms} ms`
    meta.textContent = `payload ${record.payload_bytes} B · ws ${record.ws_message_bytes} B · 间隔 ${interval}`
    const body = document.createElement('pre')
    body.className = 'observer-body'
    if (record.text !== null) {
      body.textContent = record.text
    } else {
      body.textContent = `nonce: ${record.nonce_b64 ?? ''}\nciphertext: ${record.ciphertext_b64 ?? ''}`
    }
    card.append(head, meta, body)
    observerMessages.append(card)
  }
  observerMessages.scrollTop = observerMessages.scrollHeight
}

function timeLabel(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }
  return date.toLocaleTimeString('zh-CN', { hour12: false })
}

function stateLabel(state: SessionView['state']): string {
  switch (state) {
    case 'INVITED':
      return '等待接受'
    case 'NEGOTIATING':
      return '协商中'
    case 'ACTIVE':
      return '可通信'
    default:
      return '已关闭'
  }
}

copyButton.addEventListener('click', () => {
  void copyURL()
})

pauseButton.addEventListener('click', () => {
  paused = !paused
  pauseButton.textContent = paused ? '恢复观察' : '暂停观察'
  if (paused) {
    observerStatus.textContent = '观察显示已暂停；聊天与统计继续在服务端进行。'
  } else {
    snapshot = null
    resubscribe()
    observerStatus.textContent = '正在重新同步观察记录…'
  }
})

sessionSelect.addEventListener('change', () => {
  if (sessionSelect.value) {
    selectSession(sessionSelect.value)
  }
})

void loadInfo().then(() => {
  connectAdmin()
})

