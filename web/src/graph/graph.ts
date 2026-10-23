import type { ParticipantView, SessionView } from '../shared/protocol'
import { assignNodePositions, GRAPH_HEIGHT, GRAPH_WIDTH, type Point } from './layout'

const SVG_NS = 'http://www.w3.org/2000/svg'

export interface GraphCallbacks {
  onSelectSession: (sessionID: string) => void
  onSelectPeer: (participantID: string) => void
}

export interface GraphOptions {
  allowAllEdges?: boolean
}

export class GraphView {
  #svg: SVGSVGElement
  #callbacks: GraphCallbacks
  #positions = new Map<string, Point>()
  #sessions = new Map<string, SessionView>()
  #pulses = new Set<string>()
  #allowAllEdges: boolean

  constructor(svg: SVGSVGElement, callbacks: GraphCallbacks, options: GraphOptions = {}) {
    this.#svg = svg
    this.#callbacks = callbacks
    this.#allowAllEdges = options.allowAllEdges ?? false
  }

  render(participants: ParticipantView[], sessions: SessionView[], selfID: string, activeSessionID: string | null): void {
    this.#positions = assignNodePositions(this.#positions, participants.map((participant) => participant.id))
    this.#sessions = new Map(sessions.map((session) => [session.session_id, session]))
    this.#svg.replaceChildren()

    const edgeLayer = createSvg('g', { class: 'graph-edges' })
    const nodeLayer = createSvg('g', { class: 'graph-nodes' })
    this.#svg.append(edgeLayer, nodeLayer)

    for (const session of sessions) {
      if (session.state === 'CLOSED') {
        continue
      }
      const a = this.#positions.get(session.a_id)
      const b = this.#positions.get(session.b_id)
      if (!a || !b) {
        continue
      }
      const own = session.a_id === selfID || session.b_id === selfID
      const clickable = own || this.#allowAllEdges
      const line = createSvg('line', {
        x1: String(a.x),
        y1: String(a.y),
        x2: String(b.x),
        y2: String(b.y),
        class: `graph-edge edge-${session.mode} state-${session.state.toLowerCase()}${own ? ' own' : ''}${session.session_id === activeSessionID ? ' active' : ''}${clickable ? ' clickable' : ''}`,
      })
      line.append(svgText('title', {}, `${session.mode === 'encrypted' ? '加密' : '明文'} · ${stateLabel(session.state)}`))
      if (clickable) {
        line.addEventListener('click', () => this.#callbacks.onSelectSession(session.session_id))
      }
      edgeLayer.append(line)

      const label = svgText('text', {
        x: String((a.x + b.x) / 2),
        y: String((a.y + b.y) / 2 - 6),
        class: `edge-label${own ? ' own' : ''}`,
      }, session.mode === 'encrypted' ? '加密' : '明文')
      edgeLayer.append(label)
    }

    for (const participant of participants) {
      const point = this.#positions.get(participant.id)
      if (!point) {
        continue
      }
      const group = createSvg('g', {
        class: `graph-node${participant.id === selfID ? ' self' : ''}`,
        transform: `translate(${point.x} ${point.y})`,
        tabindex: '0',
        role: 'button',
        'aria-label': `${participant.nickname} ${participant.short_id}`,
      })
      group.append(svgText('title', {}, `${participant.nickname} · ${participant.short_id}`))
      group.append(createSvg('circle', {
        r: '27',
        fill: participant.color,
        class: 'node-circle',
      }))
      group.append(svgText('text', { y: '46', class: 'node-name' }, participant.nickname))
      group.append(svgText('text', { y: '62', class: 'node-code' }, participant.short_id))
      if (participant.id !== selfID) {
        group.addEventListener('click', () => this.#callbacks.onSelectPeer(participant.id))
        group.addEventListener('keydown', (event) => {
          if (event.key === 'Enter' || event.key === ' ') {
            event.preventDefault()
            this.#callbacks.onSelectPeer(participant.id)
          }
        })
      }
      nodeLayer.append(group)
    }
  }

  reset(): void {
    this.#positions.clear()
    this.#sessions.clear()
    this.#svg.replaceChildren()
  }

  pulse(sessionID: string, direction: 'a_to_b' | 'b_to_a'): void {
    if (this.#pulses.has(sessionID)) {
      return
    }
    const session = this.#sessions.get(sessionID)
    if (!session) {
      return
    }
    const a = this.#positions.get(session.a_id)
    const b = this.#positions.get(session.b_id)
    if (!a || !b) {
      return
    }
    const from = direction === 'a_to_b' ? a : b
    const to = direction === 'a_to_b' ? b : a
    const circle = createSvg('circle', { r: '7', class: `graph-pulse pulse-${session.mode}` })
    const motion = createSvg('animateMotion', {
      dur: '0.7s',
      path: `M ${from.x} ${from.y} L ${to.x} ${to.y}`,
      fill: 'freeze',
    })
    circle.append(motion)
    const layer = this.#svg.querySelector('.graph-edges')
    layer?.append(circle)
    this.#pulses.add(sessionID)
    window.setTimeout(() => {
      this.#pulses.delete(sessionID)
      circle.remove()
    }, 750)
  }
}

function createSvg<K extends keyof SVGElementTagNameMap>(tag: K, attributes: Record<string, string>): SVGElementTagNameMap[K] {
  const element = document.createElementNS(SVG_NS, tag) as SVGElementTagNameMap[K]
  for (const [key, value] of Object.entries(attributes)) {
    element.setAttribute(key, value)
  }
  return element
}

function svgText(tag: 'text' | 'title', attributes: Record<string, string>, text: string): SVGTextElement | SVGTitleElement {
  const element = createSvg(tag, attributes)
  element.textContent = text
  return element
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

export { GRAPH_HEIGHT, GRAPH_WIDTH }


