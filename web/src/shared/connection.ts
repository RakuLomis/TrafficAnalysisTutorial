import { parseServerMessage, type ServerMessage } from './protocol'

export interface ConnectionHandlers {
  onOpen: () => void
  onMessage: (message: ServerMessage) => void
  onClose: () => void
  onError?: (message: string) => void
}

export class ClassroomConnection {
  #handlers: ConnectionHandlers
  #heartbeatMs: number
  #socket: WebSocket | null = null
  #heartbeat: number | null = null
  #closed = false

  constructor(handlers: ConnectionHandlers, heartbeatMs = 15000) {
    this.#handlers = handlers
    this.#heartbeatMs = heartbeatMs
  }

  connect(): void {
    this.#closed = false
    const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const socket = new WebSocket(`${scheme}//${location.host}/ws`)
    this.#socket = socket

    socket.addEventListener('open', () => {
      this.#startHeartbeat()
      this.#handlers.onOpen()
    })
    socket.addEventListener('message', (event) => {
      if (typeof event.data !== 'string') {
        return
      }
      const message = parseServerMessage(event.data)
      if (message) {
        this.#handlers.onMessage(message)
      }
    })
    socket.addEventListener('error', () => {
      this.#handlers.onError?.('WebSocket 连接错误')
    })
    socket.addEventListener('close', () => {
      this.#stopHeartbeat()
      if (this.#closed) {
        return
      }
      this.#closed = true
      this.#handlers.onClose()
    })
  }

  send(message: object): boolean {
    if (!this.#socket || this.#socket.readyState !== WebSocket.OPEN) {
      return false
    }
    try {
      this.#socket.send(JSON.stringify(message))
      return true
    } catch {
      return false
    }
  }

  close(): void {
    this.#closed = true
    this.#stopHeartbeat()
    this.#socket?.close()
    this.#socket = null
  }

  #startHeartbeat(): void {
    this.#stopHeartbeat()
    this.#heartbeat = window.setInterval(() => {
      this.send({ v: 1, type: 'ping' })
    }, this.#heartbeatMs)
  }

  #stopHeartbeat(): void {
    if (this.#heartbeat !== null) {
      window.clearInterval(this.#heartbeat)
      this.#heartbeat = null
    }
  }
}
