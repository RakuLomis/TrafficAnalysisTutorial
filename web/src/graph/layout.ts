export interface Point {
  x: number
  y: number
}

export const GRAPH_WIDTH = 1000
export const GRAPH_HEIGHT = 1000
export const GRAPH_MARGIN = 70

const CENTER_X = GRAPH_WIDTH / 2
const CENTER_Y = GRAPH_HEIGHT / 2
const GOLDEN_ANGLE = 137.508

const RINGS = [
  { radius: 120, capacity: 8 },
  { radius: 200, capacity: 14 },
  { radius: 280, capacity: 20 },
  { radius: 360, capacity: 26 },
  { radius: 430, capacity: 32 },
] as const

const RING_ORDER = [0, 2, 4, 1, 3] as const

export function goldenAngleSlots(): Point[] {
  const slots: Point[] = []
  const maxCapacity = Math.max(...RINGS.map((ring) => ring.capacity))
  for (let index = 0; index < maxCapacity; index++) {
    for (const ringIndex of RING_ORDER) {
      const ring = RINGS[ringIndex]
      if (index >= ring.capacity) {
        continue
      }
      const angle = ((ringIndex * GOLDEN_ANGLE + (index * 360) / ring.capacity) * Math.PI) / 180
      slots.push({
        x: CENTER_X + ring.radius * Math.cos(angle),
        y: CENTER_Y + ring.radius * Math.sin(angle),
      })
    }
  }
  return slots
}

export function assignNodePositions(existing: Map<string, Point>, ids: string[]): Map<string, Point> {
  const next = new Map(existing)
  const used = new Set([...next.values()].map(pointKey))
  const slots = goldenAngleSlots()
  let slotIndex = 0
  for (const id of ids) {
    if (next.has(id)) {
      continue
    }
    while (slotIndex < slots.length) {
      const point = slots[slotIndex]
      slotIndex++
      if (!used.has(pointKey(point))) {
        next.set(id, point)
        used.add(pointKey(point))
        break
      }
    }
    if (!next.has(id)) {
      next.set(id, fallbackPoint(id))
    }
  }
  return next
}

function fallbackPoint(id: string): Point {
  let hash = 0
  for (let i = 0; i < id.length; i++) {
    hash = (hash * 31 + id.charCodeAt(i)) >>> 0
  }
  const width = GRAPH_WIDTH - GRAPH_MARGIN * 2
  const height = GRAPH_HEIGHT - GRAPH_MARGIN * 2
  return {
    x: GRAPH_MARGIN + (hash % Math.floor(width)),
    y: GRAPH_MARGIN + ((hash >>> 8) % Math.floor(height)),
  }
}

function pointKey(point: Point): string {
  return `${Math.round(point.x)}:${Math.round(point.y)}`
}
