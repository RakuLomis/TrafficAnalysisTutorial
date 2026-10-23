import { describe, expect, it } from 'vitest'
import { clampPan, clampZoom, fitTransform, MAX_ZOOM, MIN_ZOOM, zoomAt } from './zoom'

describe('zoomAt', () => {
  it('keeps the anchor point fixed while zooming', () => {
    const before = { x: 0, y: 0, scale: 1 }
    const anchor = { x: 500, y: 400 }
    const after = zoomAt(before, 1.5, anchor)
    const worldBefore = {
      x: (anchor.x - before.x) / before.scale,
      y: (anchor.y - before.y) / before.scale,
    }
    const worldAfter = {
      x: (anchor.x - after.x) / after.scale,
      y: (anchor.y - after.y) / after.scale,
    }
    expect(worldAfter.x).toBeCloseTo(worldBefore.x)
    expect(worldAfter.y).toBeCloseTo(worldBefore.y)
  })
})

describe('clampZoom', () => {
  it('clamps to the configured range', () => {
    expect(clampZoom(0.1)).toBe(MIN_ZOOM)
    expect(clampZoom(9)).toBe(MAX_ZOOM)
  })
})

describe('fitTransform', () => {
  it('centers a bounding box and respects max scale', () => {
    const fitted = fitTransform(
      { minX: 100, minY: 100, maxX: 300, maxY: 300 },
      { width: 1000, height: 1000 },
      50,
      2,
    )
    expect(fitted.scale).toBe(2)
    expect(fitted.x).toBe(1000 / 2 - 200 * 2)
    expect(fitted.y).toBe(1000 / 2 - 200 * 2)
  })
})

describe('clampPan', () => {
  it('keeps the graph center inside the viewport', () => {
    const bounds = { minX: 100, minY: 100, maxX: 300, maxY: 300 }
    const viewport = { width: 1000, height: 1000 }
    const clamped = clampPan({ x: 9999, y: -9999, scale: 2 }, bounds, viewport)
    expect(clamped.x).toBeLessThanOrEqual(1000 - 200 * 2)
    expect(clamped.y).toBeGreaterThanOrEqual(-200 * 2)
  })
})
