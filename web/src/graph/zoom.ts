export interface Point {
  x: number
  y: number
}

export interface Transform {
  x: number
  y: number
  scale: number
}

export interface Bounds {
  minX: number
  minY: number
  maxX: number
  maxY: number
}

export interface Size {
  width: number
  height: number
}

export const MIN_ZOOM = 0.6
export const MAX_ZOOM = 3

export function clampZoom(scale: number): number {
  return Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, scale))
}

export function zoomAt(current: Transform, factor: number, anchor: Point): Transform {
  const nextScale = clampZoom(current.scale * factor)
  const ratio = nextScale / current.scale
  return {
    x: anchor.x - (anchor.x - current.x) * ratio,
    y: anchor.y - (anchor.y - current.y) * ratio,
    scale: nextScale,
  }
}

export function fitTransform(bounds: Bounds, viewport: Size, padding: number, maxScale = 1): Transform {
  const width = Math.max(1, bounds.maxX - bounds.minX)
  const height = Math.max(1, bounds.maxY - bounds.minY)
  const availableWidth = Math.max(1, viewport.width - padding * 2)
  const availableHeight = Math.max(1, viewport.height - padding * 2)
  const scale = clampZoom(Math.min(availableWidth / width, availableHeight / height, maxScale))
  const centerX = (bounds.minX + bounds.maxX) / 2
  const centerY = (bounds.minY + bounds.maxY) / 2
  return {
    x: viewport.width / 2 - centerX * scale,
    y: viewport.height / 2 - centerY * scale,
    scale,
  }
}

export function clampPan(current: Transform, bounds: Bounds, viewport: Size): Transform {
  const centerX = ((bounds.minX + bounds.maxX) / 2) * current.scale
  const centerY = ((bounds.minY + bounds.maxY) / 2) * current.scale
  const minX = -centerX
  const maxX = viewport.width - centerX
  const minY = -centerY
  const maxY = viewport.height - centerY
  return {
    x: Math.min(maxX, Math.max(minX, current.x)),
    y: Math.min(maxY, Math.max(minY, current.y)),
    scale: current.scale,
  }
}
