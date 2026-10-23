import type { ParticipantView, SessionView } from '../shared/protocol'
import { assignNodePositions, GRAPH_HEIGHT, GRAPH_WIDTH, type Point } from './layout'
import { clampPan, clampZoom, fitTransform, zoomAt, type Bounds, type Transform } from './zoom'

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
  #allowAllEdges: boolean
  #positions = new Map<string, Point>()
  #sessions = new Map<string, SessionView>()
  #pulses = new Set<string>()
  #nodes = new Map<string, SVGGElement>()
  #viewport: SVGGElement | null = null
  #transform: Transform = { x: 0, y: 0, scale: 1 }
  #pendingTransform: Transform | null = null
  #raf = 0
  #participantCount = 0
  #toolbar: HTMLDivElement | null = null
  #zoomLabel: HTMLSpanElement | null = null
  #fullscreen = false
  #pointers = new Map<number, { x: number; y: number }>()
  #drag: { active: boolean; lastX: number; lastY: number } = { active: false, lastX: 0, lastY: 0 }
  #pinch: { active: boolean; distance: number; center: Point } = {
    active: false,
    distance: 0,
    center: { x: 0, y: 0 },
  }

  constructor(svg: SVGSVGElement, callbacks: GraphCallbacks, options: GraphOptions = {}) {
    this.#svg = svg
    this.#callbacks = callbacks
    this.#allowAllEdges = options.allowAllEdges ?? false
    this.#svg.tabIndex = 0
    this.#ensureToolbar()
    this.#bindGestures()
  }

  render(participants: ParticipantView[], sessions: SessionView[], selfID: string, activeSessionID: string | null): void {
    this.#participantCount = participants.length
    this.#positions = assignNodePositions(this.#positions, participants.map((participant) => participant.id))
    this.#sessions = new Map(sessions.map((session) => [session.session_id, session]))
    this.#svg.replaceChildren()
    this.#nodes.clear()

    const edgeLayer = createSvg('g', { class: 'graph-edges' })
    const hitLayer = createSvg('g', { class: 'graph-edge-hits' })
    const nodeLayer = createSvg('g', { class: 'graph-nodes' })
    const viewport = createSvg('g', { class: 'graph-viewport' })
    viewport.append(edgeLayer, hitLayer, nodeLayer)
    this.#svg.append(viewport)
    this.#viewport = viewport

    const activeSession = activeSessionID ? this.#sessions.get(activeSessionID) : undefined
    const focusIDs = new Set<string>()
    if (activeSession) {
      focusIDs.add(activeSession.a_id)
      focusIDs.add(activeSession.b_id)
    }

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
      const active = session.session_id === activeSessionID
      const line = createSvg('line', {
        x1: String(a.x),
        y1: String(a.y),
        x2: String(b.x),
        y2: String(b.y),
        class: `graph-edge edge-${session.mode} state-${session.state.toLowerCase()}${own ? ' own' : ''}${active ? ' active' : ''}${clickable ? ' clickable' : ''}`,
      })
      line.setAttribute('data-session-id', session.session_id)
      line.append(svgText('title', {}, `${session.mode === 'encrypted' ? '加密' : '明文'} · ${stateLabel(session.state)}`))
      edgeLayer.append(line)

      if (clickable) {
        const hit = createSvg('line', {
          x1: String(a.x),
          y1: String(a.y),
          x2: String(b.x),
          y2: String(b.y),
          class: 'edge-hit',
        })
        hit.setAttribute('data-session-id', session.session_id)
        hit.addEventListener('mouseenter', () => {
          line.classList.add('hover')
          this.#nodeAt(session.a_id)?.classList.add('hover')
          this.#nodeAt(session.b_id)?.classList.add('hover')
        })
        hit.addEventListener('mouseleave', () => {
          line.classList.remove('hover')
          this.#nodeAt(session.a_id)?.classList.remove('hover')
          this.#nodeAt(session.b_id)?.classList.remove('hover')
        })
        hit.addEventListener('click', (event) => {
          event.stopPropagation()
          this.#callbacks.onSelectSession(session.session_id)
        })
        hitLayer.append(hit)
      }

      const labelX = (a.x + b.x) / 2
      const labelY = (a.y + b.y) / 2
      const label = svgText('text', {
        x: String(labelX),
        y: String(labelY - 10),
        class: `edge-label${own ? ' own' : ''}${active ? ' active' : ''}`,
      }, session.mode === 'encrypted' ? '加密' : '明文')
      edgeLayer.append(label)
    }

    for (const participant of participants) {
      const point = this.#positions.get(participant.id)
      if (!point) {
        continue
      }
      const focus = focusIDs.has(participant.id)
      const group = createSvg('g', {
        class: `graph-node${participant.id === selfID ? ' self' : ''}${focus ? ' focus' : ''}`,
        transform: `translate(${point.x} ${point.y})`,
        tabindex: '0',
        role: 'button',
        'aria-label': `${participant.nickname} ${participant.short_id}`,
      })
      group.setAttribute('data-id', participant.id)
      group.append(svgText('title', {}, `${participant.nickname} · ${participant.short_id}`))
      group.append(createSvg('circle', {
        r: '30',
        fill: participant.color,
        class: 'node-circle',
      }))
      group.append(svgText('text', { y: '50', class: 'node-name' }, participant.nickname))
      group.append(svgText('text', { y: '67', class: 'node-code' }, participant.short_id))
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
      this.#nodes.set(participant.id, group)
    }

    this.#svg.classList.toggle('labels-minimal', this.#shouldMinimalLabels())
    this.#applyTransform()
  }

  reset(): void {
    this.#positions.clear()
    this.#sessions.clear()
    this.#svg.replaceChildren()
    this.#viewport = null
    this.#nodes.clear()
    this.#transform = { x: 0, y: 0, scale: 1 }
    this.#pendingTransform = null
    this.#applyTransform()
    this.#exitFullscreen()
  }

  fit(): void {
    const bounds = this.#graphBounds()
    if (!bounds) {
      this.#animateTo({ x: 0, y: 0, scale: 1 })
      return
    }
    this.#animateTo(fitTransform(bounds, { width: GRAPH_WIDTH, height: GRAPH_HEIGHT }, 80, 1.15))
  }

  focusSession(sessionID: string): void {
    const session = this.#sessions.get(sessionID)
    if (!session) {
      return
    }
    const a = this.#positions.get(session.a_id)
    const b = this.#positions.get(session.b_id)
    if (!a || !b) {
      return
    }
    const bounds: Bounds = {
      minX: Math.min(a.x, b.x) - 90,
      minY: Math.min(a.y, b.y) - 90,
      maxX: Math.max(a.x, b.x) + 90,
      maxY: Math.max(a.y, b.y) + 90,
    }
    this.#animateTo(fitTransform(bounds, { width: GRAPH_WIDTH, height: GRAPH_HEIGHT }, 60, 2.2))
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
    this.#viewport?.append(circle)
    this.#pulses.add(sessionID)
    window.setTimeout(() => {
      this.#pulses.delete(sessionID)
      circle.remove()
    }, 750)
  }

  #nodeAt(id: string): SVGGElement | undefined {
    return this.#nodes.get(id)
  }

  #shouldMinimalLabels(): boolean {
    return this.#transform.scale < 0.9 || this.#participantCount > 40
  }

  #graphBounds(): Bounds | null {
    if (this.#positions.size === 0) {
      return null
    }
    let minX = Infinity
    let minY = Infinity
    let maxX = -Infinity
    let maxY = -Infinity
    for (const point of this.#positions.values()) {
      minX = Math.min(minX, point.x)
      minY = Math.min(minY, point.y)
      maxX = Math.max(maxX, point.x)
      maxY = Math.max(maxY, point.y)
    }
    return { minX, minY, maxX, maxY }
  }

  #ensureToolbar(): void {
    const parent = this.#svg.parentElement
    if (!parent) {
      return
    }
    parent.querySelector('.graph-toolbar')?.remove()
    const toolbar = document.createElement('div')
    toolbar.className = 'graph-toolbar'
    toolbar.setAttribute('role', 'toolbar')
    toolbar.setAttribute('aria-label', '拓扑缩放')
    this.#toolbar = toolbar

    const out = button('−', '缩小')
    const label = document.createElement('button')
    label.type = 'button'
    label.className = 'graph-zoom-label'
    label.title = '复位到 100%'
    label.textContent = '100%'
    this.#zoomLabel = label
    const zoomIn = button('+', '放大')
    const fit = button('适应', '适应窗口')
    const fullscreen = button('⤢', '全屏')
    fullscreen.className = 'graph-tool-button graph-fullscreen-button'

    out.addEventListener('click', () => this.#zoomBy(1 / 1.2))
    zoomIn.addEventListener('click', () => this.#zoomBy(1.2))
    label.addEventListener('click', () => this.#animateTo({ x: 0, y: 0, scale: 1 }))
    fit.addEventListener('click', () => this.fit())
    fullscreen.addEventListener('click', () => this.#toggleFullscreen())

    toolbar.append(out, label, zoomIn, fit, fullscreen)
    parent.append(toolbar)
  }

  #zoomBy(factor: number): void {
    this.#animateTo(zoomAt(this.#transform, factor, { x: GRAPH_WIDTH / 2, y: GRAPH_HEIGHT / 2 }))
  }

  #bindGestures(): void {
    this.#svg.addEventListener('wheel', (event) => {
      event.preventDefault()
      const anchor = this.#pointFromEvent(event)
      const factor = event.deltaY < 0 ? 1.12 : 1 / 1.12
      this.#setTransform(zoomAt(this.#transform, factor, anchor), false)
    }, { passive: false })

    this.#svg.addEventListener('pointerdown', (event) => {
      this.#svg.setPointerCapture(event.pointerId)
      this.#pointers.set(event.pointerId, { x: event.clientX, y: event.clientY })
      if (this.#pointers.size === 1) {
        this.#drag = { active: true, lastX: event.clientX, lastY: event.clientY }
        this.#svg.classList.add('panning')
      } else if (this.#pointers.size === 2) {
        this.#drag.active = false
        const [first, second] = [...this.#pointers.values()]
        this.#pinch = {
          active: true,
          distance: Math.hypot(first.x - second.x, first.y - second.y),
          center: this.#pointFromClient((first.x + second.x) / 2, (first.y + second.y) / 2),
        }
      }
    })

    this.#svg.addEventListener('pointermove', (event) => {
      if (!this.#pointers.has(event.pointerId)) {
        return
      }
      this.#pointers.set(event.pointerId, { x: event.clientX, y: event.clientY })
      if (this.#pinch.active && this.#pointers.size >= 2) {
        const [first, second] = [...this.#pointers.values()]
        const distance = Math.hypot(first.x - second.x, first.y - second.y)
        if (this.#pinch.distance > 0) {
          const center = this.#pointFromClient((first.x + second.x) / 2, (first.y + second.y) / 2)
          const factor = distance / this.#pinch.distance
          const zoomed = zoomAt(this.#transform, factor, center)
          zoomed.x += center.x - this.#pinch.center.x
          zoomed.y += center.y - this.#pinch.center.y
          this.#setTransform(zoomed, false)
          this.#pinch = { active: true, distance, center }
        }
        return
      }
      if (this.#drag.active) {
        const rect = this.#svg.getBoundingClientRect()
        const dx = ((event.clientX - this.#drag.lastX) * GRAPH_WIDTH) / rect.width
        const dy = ((event.clientY - this.#drag.lastY) * GRAPH_HEIGHT) / rect.height
        this.#drag.lastX = event.clientX
        this.#drag.lastY = event.clientY
        this.#setTransform({ x: this.#transform.x + dx, y: this.#transform.y + dy, scale: this.#transform.scale }, false)
      }
    })

    const release = (event: PointerEvent) => {
      this.#pointers.delete(event.pointerId)
      if (this.#pointers.size < 2) {
        this.#pinch.active = false
      }
      if (this.#pointers.size === 0) {
        this.#drag.active = false
        this.#svg.classList.remove('panning')
      }
    }
    this.#svg.addEventListener('pointerup', release)
    this.#svg.addEventListener('pointercancel', release)

    this.#svg.addEventListener('dblclick', (event) => {
      if ((event.target as Element).closest('[data-session-id]') || (event.target as Element).closest('.graph-node')) {
        return
      }
      this.fit()
    })

    this.#svg.addEventListener('keydown', (event) => {
      if (event.key === '+' || event.key === '=') {
        event.preventDefault()
        this.#zoomBy(1.2)
      } else if (event.key === '-') {
        event.preventDefault()
        this.#zoomBy(1 / 1.2)
      } else if (event.key === '0') {
        event.preventDefault()
        this.fit()
      } else if (event.key.toLowerCase() === 'f') {
        event.preventDefault()
        this.#toggleFullscreen()
      }
    })

    document.addEventListener('keydown', (event) => {
      if (event.key === 'Escape' && this.#fullscreen) {
        this.#exitFullscreen()
      }
    })
  }

  #pointFromEvent(event: MouseEvent | WheelEvent): Point {
    return this.#pointFromClient(event.clientX, event.clientY)
  }

  #pointFromClient(clientX: number, clientY: number): Point {
    const rect = this.#svg.getBoundingClientRect()
    return {
      x: ((clientX - rect.left) * GRAPH_WIDTH) / rect.width,
      y: ((clientY - rect.top) * GRAPH_HEIGHT) / rect.height,
    }
  }

  #setTransform(next: Transform, animate = true): void {
    const bounds = this.#graphBounds()
    const scale = clampZoom(next.scale)
    let candidate: Transform = { x: next.x, y: next.y, scale }
    if (bounds) {
      candidate = clampPan(candidate, bounds, { width: GRAPH_WIDTH, height: GRAPH_HEIGHT })
    }
    if (animate) {
      this.#animateTo(candidate)
      return
    }
    this.#pendingTransform = candidate
    if (this.#raf) {
      return
    }
    this.#raf = requestAnimationFrame(() => {
      this.#raf = 0
      if (this.#pendingTransform) {
        this.#transform = this.#pendingTransform
        this.#pendingTransform = null
        this.#applyTransform()
      }
    })
  }

  #animateTo(target: Transform): void {
    this.#pendingTransform = null
    if (this.#raf) {
      cancelAnimationFrame(this.#raf)
      this.#raf = 0
    }
    const bounds = this.#graphBounds()
    const clamped = bounds
      ? clampPan({ ...target, scale: clampZoom(target.scale) }, bounds, { width: GRAPH_WIDTH, height: GRAPH_HEIGHT })
      : { ...target, scale: clampZoom(target.scale) }
    if (matchMedia('(prefers-reduced-motion: reduce)').matches) {
      this.#transform = clamped
      this.#applyTransform()
      return
    }
    const start = this.#transform
    const startedAt = performance.now()
    const duration = 180
    const step = (now: number) => {
      const t = Math.min(1, (now - startedAt) / duration)
      const eased = 1 - Math.pow(1 - t, 3)
      this.#transform = {
        x: start.x + (clamped.x - start.x) * eased,
        y: start.y + (clamped.y - start.y) * eased,
        scale: start.scale + (clamped.scale - start.scale) * eased,
      }
      this.#applyTransform()
      if (t < 1) {
        requestAnimationFrame(step)
      }
    }
    requestAnimationFrame(step)
  }

  #applyTransform(): void {
    this.#viewport?.setAttribute('transform', `translate(${this.#transform.x} ${this.#transform.y}) scale(${this.#transform.scale})`)
    if (this.#zoomLabel) {
      this.#zoomLabel.textContent = `${Math.round(this.#transform.scale * 100)}%`
    }
    this.#svg.classList.toggle('labels-minimal', this.#shouldMinimalLabels())
  }

  #toggleFullscreen(): void {
    const card = this.#svg.parentElement
    if (!card) {
      return
    }
    if (this.#fullscreen) {
      this.#exitFullscreen()
      return
    }
    this.#fullscreen = true
    card.classList.add('graph-fullscreen')
    document.body.classList.add('graph-fullscreen-open')
    const button = this.#toolbar?.querySelector<HTMLButtonElement>('.graph-fullscreen-button')
    if (button) {
      button.textContent = '×'
      button.setAttribute('aria-label', '退出全屏')
    }
    window.requestAnimationFrame(() => this.fit())
  }

  #exitFullscreen(): void {
    if (!this.#fullscreen) {
      return
    }
    this.#fullscreen = false
    this.#svg.parentElement?.classList.remove('graph-fullscreen')
    document.body.classList.remove('graph-fullscreen-open')
    const button = this.#toolbar?.querySelector<HTMLButtonElement>('.graph-fullscreen-button')
    if (button) {
      button.textContent = '⤢'
      button.setAttribute('aria-label', '全屏')
    }
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

function button(text: string, label: string): HTMLButtonElement {
  const element = document.createElement('button')
  element.type = 'button'
  element.className = 'graph-tool-button'
  element.textContent = text
  element.setAttribute('aria-label', label)
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

