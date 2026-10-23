export interface Point {
  x: number
  y: number
}

export const GRAPH_WIDTH = 1000
export const GRAPH_HEIGHT = 1000
export const GRAPH_COLUMNS = 10
export const GRAPH_ROWS = 10
export const GRAPH_MARGIN = 70

export function assignNodePositions(existing: Map<string, Point>, ids: string[]): Map<string, Point> {
  const next = new Map(existing)
  const used = new Set([...next.values()].map(pointKey))
  for (const id of ids) {
    if (next.has(id)) {
      continue
    }
    let slot = 0
    while (slot < GRAPH_COLUMNS * GRAPH_ROWS) {
      const point = slotPoint(slot)
      if (!used.has(pointKey(point))) {
        next.set(id, point)
        used.add(pointKey(point))
        break
      }
      slot++
    }
    if (!next.has(id)) {
      next.set(id, fallbackPoint(id))
    }
  }
  return next
}

export function slotPoint(slot: number): Point {
  const column = slot % GRAPH_COLUMNS
  const row = Math.floor(slot / GRAPH_COLUMNS) % GRAPH_ROWS
  const width = GRAPH_WIDTH - GRAPH_MARGIN * 2
  const height = GRAPH_HEIGHT - GRAPH_MARGIN * 2
  return {
    x: GRAPH_MARGIN + (column * width) / (GRAPH_COLUMNS - 1),
    y: GRAPH_MARGIN + (row * height) / (GRAPH_ROWS - 1),
  }
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
