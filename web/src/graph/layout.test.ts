import { describe, expect, it } from 'vitest'
import { assignNodePositions, GRAPH_COLUMNS, GRAPH_ROWS, slotPoint } from './layout'

describe('assignNodePositions', () => {
  it('assigns stable positions without moving existing nodes', () => {
    const positions = assignNodePositions(new Map(), ['a', 'b'])
    const next = assignNodePositions(positions, ['a', 'b', 'c'])
    expect(next.get('a')).toEqual(positions.get('a'))
    expect(next.get('b')).toEqual(positions.get('b'))
    expect(next.get('c')).not.toEqual(next.get('a'))
    expect(next.get('c')).not.toEqual(next.get('b'))
  })

  it('does not reuse occupied slots and falls back when full', () => {
    const ids = Array.from({ length: GRAPH_COLUMNS * GRAPH_ROWS }, (_, index) => `id-${index}`)
    const positions = assignNodePositions(new Map(), ids)
    expect(positions.size).toBe(ids.length)
    const used = new Set([...positions.values()].map((point) => `${point.x}:${point.y}`))
    expect(used.size).toBe(ids.length)
    expect(positions.get('id-0')).toEqual(slotPoint(0))
  })
})

