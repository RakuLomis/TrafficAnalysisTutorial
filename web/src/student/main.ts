import { fetchConfig, type PublicConfig } from '../shared/api'
import { ClassroomConnection } from '../shared/connection'
import { ClassroomStore, type ChatMessage, type Identity, type MessageStatus } from '../shared/store'
import { GraphView } from '../graph/graph'
import { randomHex, utf8ByteLength } from '../shared/random'
import { checkCryptoCapabilities } from '../crypto/capabilities'
import { StudentCrypto } from './sessionCrypto'
import type { CryptoBasis } from './sessionCrypto'
import type {
  ActivityBatch,
  ChatAccepted,
  ChatDeliver,
  ChatStatus,
  ErrorMessage,
  KeyConfirmMessage,
  KeyPublishMessage,
  ParticipantView,
  RoundEnded,
  ServerMessage,
  SessionChanged,
  SessionView,
  TopologyChanged,
  Welcome,
} from '../shared/protocol'

function byId<T extends HTMLElement>(id: string): T {
  const element = document.getElementById(id)
  if (!element) {
    throw new Error(`missing element: ${id}`)
  }
  return element as T
}

const joinView = byId<HTMLElement>('join-view')
const appView = byId<HTMLElement>('app-view')
const configStatus = byId<HTMLParagraphElement>('config-status')
const joinStatus = byId<HTMLParagraphElement>('join-status')
const cryptoStatus = byId<HTMLParagraphElement>('crypto-status')
const joinForm = byId<HTMLFormElement>('join-form')
const nicknameInput = byId<HTMLInputElement>('nickname')
const joinButton = byId<HTMLButtonElement>('join-button')
const connectionStatus = byId<HTMLElement>('connection-status')
const selfBadge = byId<HTMLElement>('self-badge')
const selfName = byId<HTMLElement>('self-name')
const topologySvg = document.getElementById('topology') as unknown as SVGSVGElement
const topologyEmpty = byId<HTMLParagraphElement>('topology-empty')
const peerList = byId<HTMLDivElement>('peer-list')
const inviteTarget = byId<HTMLParagraphElement>('invite-target')
const inviteMode = byId<HTMLSelectElement>('invite-mode')
const inviteButton = byId<HTMLButtonElement>('invite-button')
const invites = byId<HTMLDivElement>('invites')
const sessionTabs = byId<HTMLDivElement>('session-tabs')
const chatTitle = byId<HTMLElement>('chat-title')
const chatState = byId<HTMLElement>('chat-state')
const closeSessionButton = byId<HTMLButtonElement>('close-session-button')
const messages = byId<HTMLDivElement>('messages')
const chatForm = byId<HTMLFormElement>('chat-form')
const chatInput = byId<HTMLTextAreaElement>('chat-input')
const byteCount = byId<HTMLElement>('byte-count')
const sendButton = byId<HTMLButtonElement>('send-button')
const toast = byId<HTMLDivElement>('toast')

const store = new ClassroomStore()
const graph = new GraphView(topologySvg, {
  onSelectSession: (sessionID) => selectSession(sessionID),
  onSelectPeer: (participantID) => selectPeer(participantID),
})
const crypto = new StudentCrypto((message) => connection?.send(message) ?? false)

let config: PublicConfig | null = null
let connection: ClassroomConnection | null = null
let basis: CryptoBasis | null = null
let selectedPeerID: string | null = null
let joinRequestID: string | null = null
let composing = false
let toastTimer = 0
let suppressCloseNotice = false

store.subscribe(render)

function setJoinStatus(text: string, isError = false): void {
  joinStatus.textContent = text
  joinStatus.classList.toggle('error', isError)
}

function showToast(text: string): void {
  toast.textContent = text
  toast.hidden = false
  window.clearTimeout(toastTimer)
  toastTimer = window.setTimeout(() => {
    toast.hidden = true
  }, 4200)
}

function currentIdentity(): Identity | null {
  return store.getState().identity
}

function currentSession(): SessionView | null {
  const state = store.getState()
  if (!state.activeSessionID) {
    return null
  }
  return state.sessions.get(state.activeSessionID) ?? null
}

function sessionMember(session: SessionView, selfID: string): boolean {
  return session.a_id === selfID || session.b_id === selfID
}

function peerID(session: SessionView, selfID: string): string {
  return session.a_id === selfID ? session.b_id : session.a_id
}

function openSessionWith(peerIDValue: string): SessionView | undefined {
  const identity = currentIdentity()
  if (!identity) {
    return undefined
  }
  return store.openSessions().find((session) => sessionMember(session, identity.participantID) && peerID(session, identity.participantID) === peerIDValue)
}

function stateLabel(state: SessionView['state']): string {
  switch (state) {
    case 'INVITED':
      return '等待回应'
    case 'NEGOTIATING':
      return '加密协商中'
    case 'ACTIVE':
      return '可通信'
    default:
      return '已关闭'
  }
}

function modeLabel(mode: SessionView['mode']): string {
  return mode === 'encrypted' ? '加密' : '明文'
}

function messageStatusLabel(status: MessageStatus): string {
  switch (status) {
    case 'SENDING':
      return '发送中'
    case 'ACCEPTED':
      return '服务端已接受'
    case 'CONFIRMED':
      return '接收端已确认'
    case 'UNCONFIRMED':
      return '未确认'
    case 'FAILED':
      return '发送失败'
    default:
      return '已接收'
  }
}

function renderConfig(): void {
  if (!config) {
    return
  }
  const roundLabel = config.round.status === 'RUNNING' ? '运行中' : '已结束'
  configStatus.textContent = `协议 v${config.protocol_version} · 课堂${roundLabel} · 轮次 ${config.round.id.slice(0, 8)}…`
  joinButton.disabled = config.round.status !== 'RUNNING'
  byteCount.textContent = `0 / ${config.limits.max_body_bytes}`
  chatInput.maxLength = config.limits.max_body_bytes

  const capabilities = checkCryptoCapabilities()
  const encryptedOption = inviteMode.querySelector<HTMLOptionElement>('option[value="encrypted"]')
  if (encryptedOption) {
    encryptedOption.disabled = !capabilities.encryption
  }
  if (!capabilities.encryption) {
    cryptoStatus.textContent = '当前浏览器缺少安全随机数或密码库，加密模式不可用；明文仍可使用。'
    cryptoStatus.classList.add('error')
    if (inviteMode.value === 'encrypted') {
      inviteMode.value = 'plaintext'
    }
  } else {
    cryptoStatus.textContent = '加密能力就绪：消息在发送端浏览器加密，接收端浏览器解密。'
    cryptoStatus.classList.remove('error')
  }
}

async function loadConfig(): Promise<void> {
  try {
    config = await fetchConfig()
    renderConfig()
    if (config.round.status === 'ENDED') {
      setJoinStatus('课堂已结束，等待教师开始新一轮。')
    }
  } catch (error) {
    configStatus.textContent = error instanceof Error ? error.message : '课堂配置读取失败'
    configStatus.classList.add('error')
    joinButton.disabled = true
  }
}

function startJoin(): void {
  if (!config || config.round.status !== 'RUNNING') {
    return
  }
  const nickname = nicknameInput.value.trim()
  const runes = [...nickname]
  if (runes.length < 1 || runes.length > 16 || /[\u0000-\u001f\u007f]/.test(nickname)) {
    setJoinStatus('昵称需为 1—16 个字符，且不能包含控制字符。', true)
    return
  }
  joinRequestID = randomHex()
  joinButton.disabled = true
  setJoinStatus('正在连接服务器…')
  const heartbeat = config.limits.heartbeat_ms > 0 ? config.limits.heartbeat_ms : 15000
  connection = new ClassroomConnection({
    onOpen: () => {
      if (!connection || !config || !joinRequestID) {
        return
      }
      const sent = connection.send({
        v: 1,
        type: 'join',
        request_id: joinRequestID,
        round_id: config.round.id,
        nickname,
      })
      if (!sent) {
        setJoinStatus('加入请求发送失败。', true)
      } else {
        setJoinStatus('已发送加入请求，等待服务器确认…')
      }
    },
    onMessage: handleServerMessage,
    onClose: handleConnectionClosed,
    onError: () => showToast('WebSocket 连接发生错误'),
  }, heartbeat)
  connection.connect()
}

function handleServerMessage(message: ServerMessage): void {
  switch (message.type) {
    case 'welcome':
      handleWelcome(message)
      break
    case 'topology.changed':
      handleTopology(message)
      break
    case 'session.changed':
      handleSessionChanged(message)
      break
    case 'key.publish':
      handleKeyPublish(message)
      break
    case 'key.confirm':
      handleKeyConfirm(message)
      break
    case 'chat.deliver':
      handleChatDeliver(message)
      break
    case 'chat.accepted':
      handleChatAccepted(message)
      break
    case 'chat.status':
      handleChatStatus(message)
      break
    case 'activity.batch':
      handleActivity(message)
      break
    case 'error':
      handleError(message)
      break
    case 'round.ended':
      handleRoundEnded(message)
      break
    case 'pong':
      break
    default:
      break
  }
}

function handleWelcome(welcome: Welcome): void {
  joinRequestID = null
  if (config) {
    config.round = welcome.round
  }
  basis = {
    roundID: welcome.round.id,
    selfID: welcome.participant_id,
    epoch: welcome.connection_epoch,
  }
  crypto.destroyAll()
  graph.reset()
  store.applyWelcome(welcome)
  joinView.hidden = true
  appView.hidden = false
  selfBadge.hidden = false
  connectionStatus.textContent = '已连接'
  connectionStatus.classList.remove('error')
  selectedPeerID = null
  setJoinStatus('')
  render()
}

function handleTopology(message: TopologyChanged): void {
  store.applyTopology(message)
  render()
}

function handleSessionChanged(message: SessionChanged): void {
  store.applySession(message.session)
  if (basis) {
    crypto.onSessionChanged(message.session, basis)
  }
  if (message.session.state === 'CLOSED') {
    crypto.destroy(message.session.session_id)
  } else if ((message.session.state === 'ACTIVE' || message.session.state === 'NEGOTIATING') && !store.getState().activeSessionID) {
    store.setActiveSession(message.session.session_id)
  }
  render()
}

function handleKeyPublish(message: KeyPublishMessage): void {
  if (basis) {
    crypto.onKeyPublish(message, basis)
  }
}

function handleKeyConfirm(message: KeyConfirmMessage): void {
  if (basis) {
    crypto.onKeyConfirm(message, basis)
  }
}

function handleChatDeliver(message: ChatDeliver): void {
  const session = store.getState().sessions.get(message.session_id)
  if (!session || !basis) {
    return
  }
  let text = message.text
  if (session.mode === 'encrypted') {
    try {
      text = crypto.decryptChat(message, basis)
    } catch {
      sendReceipt(session, message.message_id, 'crypto_failed')
      crypto.destroy(session.session_id)
      showToast('收到无法解密的密文，会话已标记失败。')
      return
    }
  }
  if (typeof text !== 'string') {
    return
  }
  store.addMessage({
    id: message.message_id,
    sessionID: message.session_id,
    senderID: message.sender_id,
    text,
    direction: 'in',
    status: 'RECEIVED',
    atUTC: message.sent_at_utc,
  })
  sendReceipt(session, message.message_id, 'processed')
  render()
}

function sendReceipt(session: SessionView, messageID: string, status: 'processed' | 'crypto_failed'): void {
  if (!connection || !basis) {
    return
  }
  connection.send({
    v: 1,
    type: 'chat.receipt',
    request_id: randomHex(),
    round_id: basis.roundID,
    connection_epoch: basis.epoch,
    session_id: session.session_id,
    message_id: messageID,
    status,
  })
}

function handleChatAccepted(message: ChatAccepted): void {
  store.updateMessageStatus(message.session_id, message.message_id, 'ACCEPTED')
  render()
}

function handleChatStatus(message: ChatStatus): void {
  const status: MessageStatus = message.status === 'confirmed'
    ? 'CONFIRMED'
    : message.status === 'unconfirmed'
      ? 'UNCONFIRMED'
      : 'FAILED'
  store.updateMessageStatus(message.session_id, message.message_id, status)
  if (status === 'FAILED') {
    showToast('有一条消息未能确认送达。')
  }
  render()
}

function handleActivity(message: ActivityBatch): void {
  for (const event of message.events) {
    graph.pulse(event.session_id, event.direction)
  }
}

function handleError(message: ErrorMessage): void {
  if (!currentIdentity()) {
    setJoinStatus(`${message.code}: ${message.message}`, true)
    joinButton.disabled = false
    joinRequestID = null
    connection?.close()
    connection = null
    return
  }
  showToast(`${message.code}: ${message.message}`)
  if (message.code === 'CRYPTO_FAILED' && message.session_id) {
    crypto.destroy(message.session_id)
    render()
  }
  if (message.code === 'STALE_CONNECTION' || message.code === 'STALE_ROUND') {
    connection?.close()
  }
}

function handleRoundEnded(message: RoundEnded): void {
  suppressCloseNotice = true
  crypto.destroyAll()
  store.reset()
  graph.reset()
  selectedPeerID = null
  basis = null
  joinRequestID = null
  appView.hidden = true
  joinView.hidden = false
  selfBadge.hidden = true
  connectionStatus.textContent = '未连接'
  connectionStatus.classList.remove('error')
  connection?.close()
  connection = null
  if (message.reason === 'class_end') {
    setJoinStatus('课堂已结束，等待教师开始新一轮。', true)
  } else {
    setJoinStatus('本轮已结束，正在读取新一轮信息…')
  }
  void loadConfig().then(() => {
    if (config?.round.status === 'RUNNING') {
      setJoinStatus('新一轮已开始，可以重新加入。')
    }
  })
}

function handleConnectionClosed(): void {
  connection = null
  if (suppressCloseNotice) {
    suppressCloseNotice = false
    return
  }
  crypto.destroyAll()
  store.reset()
  graph.reset()
  basis = null
  joinRequestID = null
  selectedPeerID = null
  appView.hidden = true
  joinView.hidden = false
  selfBadge.hidden = true
  connectionStatus.textContent = '未连接'
  connectionStatus.classList.add('error')
  setJoinStatus('连接已断开。请刷新页面重新加入；旧会话与密钥已清除。', true)
  joinButton.disabled = !config || config.round.status !== 'RUNNING'
}

function selectPeer(participantID: string): void {
  const session = openSessionWith(participantID)
  if (session) {
    selectSession(session.session_id)
    return
  }
  selectedPeerID = participantID
  render()
}

function selectSession(sessionID: string): void {
  const identity = currentIdentity()
  const session = store.getState().sessions.get(sessionID)
  if (!identity || !session || !sessionMember(session, identity.participantID)) {
    return
  }
  store.setActiveSession(sessionID)
  render()
}

function sendInvite(): void {
  const identity = currentIdentity()
  if (!identity || !selectedPeerID || !connection || !basis || !config) {
    return
  }
  if (openSessionWith(selectedPeerID)) {
    showToast('与该同学已有未关闭会话。')
    return
  }
  const mode = inviteMode.value === 'encrypted' ? 'encrypted' : 'plaintext'
  const sent = connection.send({
    v: 1,
    type: 'invite.create',
    request_id: randomHex(),
    round_id: basis.roundID,
    connection_epoch: basis.epoch,
    target_id: selectedPeerID,
    mode,
  })
  if (!sent) {
    showToast('邀请发送失败。')
    return
  }
  inviteButton.disabled = true
  showToast('邀请已发送，等待对方回应。')
  render()
}

function respondInvite(session: SessionView, accept: boolean): void {
  if (!connection || !basis) {
    return
  }
  connection.send({
    v: 1,
    type: 'invite.respond',
    request_id: randomHex(),
    round_id: basis.roundID,
    connection_epoch: basis.epoch,
    session_id: session.session_id,
    accept,
  })
}

function cancelInvite(session: SessionView): void {
  if (!connection || !basis) {
    return
  }
  connection.send({
    v: 1,
    type: 'invite.cancel',
    request_id: randomHex(),
    round_id: basis.roundID,
    connection_epoch: basis.epoch,
    session_id: session.session_id,
  })
}

function sendCurrentMessage(): void {
  const identity = currentIdentity()
  const session = currentSession()
  if (!identity || !session || !connection || !basis || !config) {
    return
  }
  if (session.state !== 'ACTIVE') {
    showToast('会话尚未激活。')
    return
  }
  const text = chatInput.value
  if (!text) {
    return
  }
  const bytes = utf8ByteLength(text)
  if (bytes > config.limits.max_body_bytes) {
    showToast(`消息超出 ${config.limits.max_body_bytes} 字节上限。`)
    return
  }
  const messageID = randomHex()
  const requestID = randomHex()
  let payload: Record<string, unknown>
  if (session.mode === 'encrypted') {
    try {
      const encrypted = crypto.encryptChat(session, basis, messageID, text)
      payload = {
        v: 1,
        type: 'chat.send',
        request_id: requestID,
        round_id: basis.roundID,
        connection_epoch: basis.epoch,
        session_id: session.session_id,
        message_id: messageID,
        seq: encrypted.seq,
        nonce_b64: encrypted.nonceB64,
        ciphertext_b64: encrypted.ciphertextB64,
      }
    } catch {
      showToast('加密会话尚未就绪，消息未发送。')
      return
    }
  } else {
    payload = {
      v: 1,
      type: 'chat.send',
      request_id: requestID,
      round_id: basis.roundID,
      connection_epoch: basis.epoch,
      session_id: session.session_id,
      message_id: messageID,
      seq: nextPlaintextSeq(session.session_id, identity.participantID),
      text,
    }
  }
  const message: ChatMessage = {
    id: messageID,
    sessionID: session.session_id,
    senderID: identity.participantID,
    text,
    direction: 'out',
    status: 'SENDING',
    atUTC: new Date().toISOString(),
  }
  store.addMessage(message)
  chatInput.value = ''
  updateByteCount()
  if (!connection.send(payload)) {
    store.updateMessageStatus(session.session_id, messageID, 'FAILED')
    showToast('消息发送失败。')
  }
  render()
}

const plaintextSeq = new Map<string, number>()

function nextPlaintextSeq(sessionID: string, senderID: string): string {
  const key = `${sessionID}|${senderID}`
  const value = plaintextSeq.get(key) ?? 0
  plaintextSeq.set(key, value + 1)
  return String(value)
}

function updateByteCount(): void {
  const max = config?.limits.max_body_bytes ?? 4096
  const bytes = utf8ByteLength(chatInput.value)
  byteCount.textContent = `${bytes} / ${max}`
  byteCount.classList.toggle('error', bytes > max)
  const active = currentSession()
  const enabled = Boolean(active && active.state === 'ACTIVE' && store.getState().connected && bytes > 0 && bytes <= max)
  sendButton.disabled = !enabled
  chatInput.disabled = !active || active.state !== 'ACTIVE' || !store.getState().connected
}

function render(): void {
  const state = store.getState()
  if (!state.identity) {
    appView.hidden = true
    joinView.hidden = false
    updateByteCount()
    return
  }
  appView.hidden = false
  joinView.hidden = true
  selfBadge.hidden = false
  selfName.textContent = `${state.identity.nickname} · #${state.identity.shortID}`
  connectionStatus.textContent = state.connected ? '已连接' : '未连接'
  connectionStatus.classList.toggle('error', !state.connected)

  const participants = [...state.participants.values()].sort((a, b) => a.short_id.localeCompare(b.short_id))
  const sessions = store.openSessions().filter((session) => sessionMember(session, state.identity!.participantID))
  graph.render(participants, sessions, state.identity.participantID, state.activeSessionID)
  topologyEmpty.hidden = participants.length > 0

  renderPeers(participants, state.identity)
  renderInvites(sessions, state.identity)
  renderTabs(sessions, state.identity)
  renderChat()
  updateByteCount()
}

function renderPeers(participants: ParticipantView[], identity: Identity): void {
  peerList.replaceChildren()
  const others = participants.filter((participant) => participant.id !== identity.participantID)
  if (others.length === 0) {
    const empty = document.createElement('p')
    empty.className = 'muted'
    empty.textContent = '暂无其他在线同学。'
    peerList.append(empty)
  }
  for (const participant of others) {
    const session = openSessionWith(participant.id)
    const button = document.createElement('button')
    button.type = 'button'
    button.className = `peer-item${selectedPeerID === participant.id && !session ? ' selected' : ''}`
    const dot = document.createElement('span')
    dot.className = 'peer-dot'
    dot.style.background = participant.color
    const label = document.createElement('span')
    label.className = 'peer-label'
    label.textContent = `${participant.nickname} · ${participant.short_id}`
    button.append(dot, label)
    if (session) {
      const badge = document.createElement('span')
      badge.className = 'peer-badge'
      badge.textContent = stateLabel(session.state)
      button.append(badge)
    }
    button.addEventListener('click', () => selectPeer(participant.id))
    peerList.append(button)
  }

  const target = selectedPeerID ? store.getState().participants.get(selectedPeerID) : null
  if (!target) {
    selectedPeerID = null
    inviteTarget.textContent = '点击在线同学选择邀请对象。'
  } else {
    inviteTarget.textContent = `邀请 ${target.nickname} · ${modeLabel(inviteMode.value === 'encrypted' ? 'encrypted' : 'plaintext')}`
  }
  inviteButton.disabled = !target || !store.getState().connected || Boolean(target && openSessionWith(target.id))
}

function renderInvites(sessions: SessionView[], identity: Identity): void {
  invites.replaceChildren()
  const pending = sessions.filter((session) => session.state === 'INVITED')
  for (const session of pending) {
    const otherID = peerID(session, identity.participantID)
    const other = store.getState().participants.get(otherID)
    const card = document.createElement('div')
    card.className = 'invite-card'
    const title = document.createElement('p')
    title.textContent = `${other?.nickname ?? '对方'} · ${modeLabel(session.mode)} · ${stateLabel(session.state)}`
    card.append(title)
    const actions = document.createElement('div')
    actions.className = 'inline-actions'
    if (session.b_id === identity.participantID) {
      const accept = document.createElement('button')
      accept.type = 'button'
      accept.textContent = '接受'
      accept.addEventListener('click', () => respondInvite(session, true))
      const reject = document.createElement('button')
      reject.type = 'button'
      reject.className = 'secondary'
      reject.textContent = '拒绝'
      reject.addEventListener('click', () => respondInvite(session, false))
      actions.append(accept, reject)
    } else {
      const cancel = document.createElement('button')
      cancel.type = 'button'
      cancel.className = 'secondary'
      cancel.textContent = '取消邀请'
      cancel.addEventListener('click', () => cancelInvite(session))
      actions.append(cancel)
    }
    card.append(actions)
    invites.append(card)
  }
}

function renderTabs(sessions: SessionView[], identity: Identity): void {
  sessionTabs.replaceChildren()
  const state = store.getState()
  const selectable = sessions.filter((session) => session.state !== 'INVITED')
  for (const session of selectable) {
    const otherID = peerID(session, identity.participantID)
    const other = store.getState().participants.get(otherID)
    const button = document.createElement('button')
    button.type = 'button'
    button.className = `session-tab${state.activeSessionID === session.session_id ? ' active' : ''}`
    button.textContent = `${other?.nickname ?? '对方'} · ${modeLabel(session.mode)} · ${stateLabel(session.state)}`
    button.addEventListener('click', () => selectSession(session.session_id))
    sessionTabs.append(button)
  }
}

function renderChat(): void {
  const state = store.getState()
  const session = currentSession()
  closeSessionButton.hidden = !session || session.state === 'CLOSED'
  if (!session) {
    chatTitle.textContent = '选择会话'
    chatState.textContent = ''
    messages.replaceChildren()
    updateByteCount()
    return
  }
  const other = state.participants.get(peerID(session, state.identity!.participantID))
  chatTitle.textContent = other?.nickname ?? '会话'
  chatState.textContent = `${modeLabel(session.mode)} · ${stateLabel(session.state)}`
  chatState.classList.toggle('error', session.state === 'CLOSED')
  messages.replaceChildren()
  const list = store.sessionMessages(session.session_id)
  for (const message of list) {
    const bubble = document.createElement('div')
    bubble.className = `message ${message.direction}`
    const text = document.createElement('div')
    text.className = 'message-text'
    text.textContent = message.text
    const meta = document.createElement('div')
    meta.className = 'message-meta'
    meta.textContent = message.direction === 'out' ? messageStatusLabel(message.status) : '已接收'
    bubble.append(text, meta)
    messages.append(bubble)
  }
  messages.scrollTop = messages.scrollHeight
  updateByteCount()
}

joinForm.addEventListener('submit', (event) => {
  event.preventDefault()
  startJoin()
})

inviteButton.addEventListener('click', sendInvite)

closeSessionButton.addEventListener('click', () => {
  const session = currentSession()
  if (!connection || !basis || !session) {
    return
  }
  connection.send({
    v: 1,
    type: 'session.close',
    request_id: randomHex(),
    round_id: basis.roundID,
    connection_epoch: basis.epoch,
    session_id: session.session_id,
    reason: 'user',
  })
})

inviteMode.addEventListener('change', render)

chatInput.addEventListener('compositionstart', () => {
  composing = true
})

chatInput.addEventListener('compositionend', () => {
  composing = false
  updateByteCount()
})

chatInput.addEventListener('input', updateByteCount)

chatInput.addEventListener('keydown', (event) => {
  if (event.key === 'Enter' && !event.shiftKey && !composing && !event.isComposing) {
    event.preventDefault()
    if (!sendButton.disabled) {
      sendCurrentMessage()
    }
  }
})

chatForm.addEventListener('submit', (event) => {
  event.preventDefault()
  sendCurrentMessage()
})

void loadConfig()

