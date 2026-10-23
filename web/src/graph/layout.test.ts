import { describe, expect, it } from 'vitest'
import { assignNodePositions, goldenAngleSlots, GRAPH_HEIGHT, GRAPH_WIDTH } from './layout'

describe('goldenAngleSlots', () => {
  it('provides 100 unique slots with reasonable spacing', () => {
    const slots = goldenAngleSlots()
    expect(slots).toHaveLength(100)
    const keys = new Set(slots.map((point) => `${Math.round(point.x)}:${Math.round(point.y)}`))
    expect(keys.size).toBe(100)
    for (const point of slots) {
      expect(point.x).toBeGreaterThanOrEqual(0)
      expect(point.x).toBeLessThanOrEqual(GRAPH_WIDTH)
      expect(point.y).toBeGreaterThanOrEqual(0)
      expect(point.y).toBeLessThanOrEqual(GRAPH_HEIGHT)
    }
    for (let i = 0; i < slots.length; i++) {
      for (let j = i + 1; j < slots.length; j++) {
        const distance = Math.hypot(slots[i].x - slots[j].x, slots[i].y - slots[j].y)
        expect(distance).toBeGreaterThan(70)
      }
    }
  })
})

describe('assignNodePositions', () => {
  it('spreads the first three nodes without collinearity', () => {
    const positions = assignNodePositions(new Map(), ['a', 'b', 'c'])
    const a = positions.get('a')!
    const b = positions.get('b')!
    const c = positions.get('c')!
    const area = Math.abs((b.x - a.x) * (c.y - a.y) - (b.y - a.y) * (c.x - a.x))
    expect(area).toBeGreaterThan(1000)
  })

  it('preserves existing positions and does not reuse slots', () => {
    const first = assignNodePositions(new Map(), ['a', 'b'])
    const second = assignNodePositions(first, ['a', 'b', 'c', 'd'])
    expect(second.get('a')).toEqual(first.get('a'))
    expect(second.get('b')).toEqual(first.get('b'))
    const keys = new Set([...second.values()].map((point) => `${point.x}:${point.y}`))
    expect(keys.size).toBe(4)
  })

  it('is deterministic', () => {
    const first = assignNodePositions(new Map(), ['a', 'b', 'c'])
    const again = assignNodePositions(new Map(), ['a', 'b', 'c'])
    expect(again).toEqual(first)
  })

  it('handles more than 100 ids without crashing', () => {
    const ids = Array.from({ length: 105 }, (_, index) => `id-${index}`)
    const positions = assignNodePositions(new Map(), ids)
    expect(positions.size).toBe(105)
  })
})
