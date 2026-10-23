import type { ParticipantView, RoundView, SessionView, TopologyChanged, Welcome } from './protocol'
import { utf8ByteLength } from './random'

export type MessageStatus = 'SENDING' | 'ACCEPTED' | 'CONFIRMED' | 'UNCONFIRMED' | 'FAILED' | 'RECEIVED'

export interface Identity {
  participantID: string
  nickname: string
  shortID: string
  color: string
  resumeToken: string
  connectionEpoch: string
}

export interface ChatMessage {
  id: string
  sessionID: string
  senderID: string
  text: string
  direction: 'in' | 'out'
  status: MessageStatus
  atUTC: string
}

export interface ClassroomState {
  identity: Identity | null
  round: RoundView | null
  topologyVersion: number
  participants: Map<string, ParticipantView>
  sessions: Map<string, SessionView>
  messages: Map<string, ChatMessage[]>
  activeSessionID: string | null
  connected: boolean
}

const MAX_SESSION_MESSAGES = 200
const MAX_CACHE_BYTES = 3 * 1024 * 1024

export class ClassroomStore {
  #state: ClassroomState = emptyState()
  #listeners = new Set<() => void>()

  getState(): ClassroomState {
    return this.#state
  }

  subscribe(listener: () => void): () => void {
    this.#listeners.add(listener)
    return () => this.#listeners.delete(listener)
  }

  applyWelcome(welcome: Welcome): void {
    this.#state = {
      identity: {
        participantID: welcome.participant_id,
        nickname: welcome.nickname,
        shortID: welcome.short_id,
        color: welcome.color,
        resumeToken: welcome.resume_token,
        connectionEpoch: welcome.connection_epoch,
      },
      round: welcome.round,
      topologyVersion: welcome.topology_version,
      participants: new Map(welcome.participants.map((participant) => [participant.id, participant])),
      sessions: new Map(welcome.sessions.map((session) => [session.session_id, session])),
      messages: new Map(),
      activeSessionID: null,
      connected: true,
    }
    this.#emit()
  }

  applyTopology(topology: TopologyChanged): void {
    if (topology.topology_version < this.#state.topologyVersion) {
      return
    }
    this.#state.topologyVersion = topology.topology_version
    this.#state.participants = new Map(topology.participants.map((participant) => [participant.id, participant]))
    this.#state.sessions = new Map(topology.sessions.map((session) => [session.session_id, session]))
    if (this.#state.activeSessionID) {
      const active = this.#state.sessions.get(this.#state.activeSessionID)
      if (!active || active.state === 'CLOSED') {
        this.#state.activeSessionID = null
      }
    }
    this.#emit()
  }

  applySession(session: SessionView): void {
    this.#state.sessions.set(session.session_id, session)
    if (!this.#state.messages.has(session.session_id)) {
      this.#state.messages.set(session.session_id, [])
    }
    if (session.state === 'CLOSED' && this.#state.activeSessionID === session.session_id) {
      this.#state.activeSessionID = null
    }
    this.#emit()
  }

  setActiveSession(sessionID: string | null): void {
    this.#state.activeSessionID = sessionID
    this.#emit()
  }

  setConnected(connected: boolean): void {
    this.#state.connected = connected
    this.#emit()
  }

  addMessage(message: ChatMessage): void {
    const list = this.#state.messages.get(message.sessionID) ?? []
    list.push(message)
    this.#state.messages.set(message.sessionID, list)
    this.#trim()
    this.#emit()
  }

  updateMessageStatus(sessionID: string, messageID: string, status: MessageStatus): void {
    const list = this.#state.messages.get(sessionID)
    if (!list) {
      return
    }
    const message = list.find((item) => item.id === messageID)
    if (!message) {
      return
    }
    message.status = status
    this.#emit()
  }

  messagesFor(sessionID: string): ChatMessage[] {
    return this.#state.messages.get(sessionID) ?? []
  }

  openSessions(): SessionView[] {
    return [...this.#state.sessions.values()].filter((session) => session.state !== 'CLOSED')
  }

  sessionMessages(sessionID: string): ChatMessage[] {
    return this.#state.messages.get(sessionID) ?? []
  }

  reset(): void {
    this.#state = emptyState()
    this.#emit()
  }

  totalMessageBytes(): number {
    let total = 0
    for (const list of this.#state.messages.values()) {
      for (const message of list) {
        total += utf8ByteLength(message.text)
      }
    }
    return total
  }

  #trim(): void {
    for (const [sessionID, list] of this.#state.messages) {
      if (list.length > MAX_SESSION_MESSAGES) {
        list.splice(0, list.length - MAX_SESSION_MESSAGES)
      }
      this.#state.messages.set(sessionID, list)
    }
    while (this.totalMessageBytes() > MAX_CACHE_BYTES) {
      let largest: ChatMessage[] | null = null
      for (const list of this.#state.messages.values()) {
        if (list.length > 0 && (!largest || list.length > largest.length)) {
          largest = list
        }
      }
      if (!largest) {
        break
      }
      largest.shift()
    }
  }

  #emit(): void {
    for (const listener of this.#listeners) {
      listener()
    }
  }
}

function emptyState(): ClassroomState {
  return {
    identity: null,
    round: null,
    topologyVersion: 0,
    participants: new Map(),
    sessions: new Map(),
    messages: new Map(),
    activeSessionID: null,
    connected: false,
  }
}
