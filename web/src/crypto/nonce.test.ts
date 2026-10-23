import { describe, expect, it } from 'vitest'
import { MAX_COUNTER, NonceAllocator, buildNonce, nonceCounter, seqMatchesNonce } from './nonce'

const prefix = new Uint8Array(15).fill(7)

describe('buildNonce', () => {
  it('lays out direction, prefix and big-endian counter', () => {
    const nonce = buildNonce(0, prefix, 1n)
    expect(nonce).toHaveLength(24)
    expect(nonce[0]).toBe(0)
    expect(nonce.slice(1, 16)).toEqual(prefix)
    expect([...nonce.slice(16)]).toEqual([0, 0, 0, 0, 0, 0, 0, 1])
    expect(nonceCounter(nonce)).toBe(1n)
  })

  it('supports both directions and max counter', () => {
    expect(buildNonce(1, prefix, 0n)[0]).toBe(1)
    expect(nonceCounter(buildNonce(0, prefix, MAX_COUNTER))).toBe(MAX_COUNTER)
  })

  it('rejects invalid input', () => {
    expect(() => buildNonce(2 as 0, prefix, 0n)).toThrow()
    expect(() => buildNonce(0, new Uint8Array(14), 0n)).toThrow()
    expect(() => buildNonce(0, prefix, -1n)).toThrow()
    expect(() => buildNonce(0, prefix, MAX_COUNTER + 1n)).toThrow()
  })
})

describe('seqMatchesNonce', () => {
  it('accepts canonical decimal only', () => {
    const nonce = buildNonce(0, prefix, 42n)
    expect(seqMatchesNonce('42', nonce)).toBe(true)
    expect(seqMatchesNonce('042', nonce)).toBe(false)
    expect(seqMatchesNonce('41', nonce)).toBe(false)
    expect(seqMatchesNonce('-1', nonce)).toBe(false)
  })
})

describe('NonceAllocator', () => {
  it('allocates increasing counters without reuse', () => {
    const allocator = new NonceAllocator(0, prefix)
    const first = allocator.allocate()
    const second = allocator.allocate()
    expect(first.seq).toBe('0')
    expect(second.seq).toBe('1')
    expect(first.nonce.slice(1, 16)).toEqual(prefix)
    expect(allocator.counter).toBe(2n)
  })

  it('separates directions', () => {
    const a = new NonceAllocator(0, prefix)
    const b = new NonceAllocator(1, prefix)
    expect(a.allocate().nonce[0]).toBe(0)
    expect(b.allocate().nonce[0]).toBe(1)
  })

  it('exhausts at uint64 max', () => {
    const allocator = new NonceAllocator(0, prefix, MAX_COUNTER)
    expect(allocator.allocate().seq).toBe(MAX_COUNTER.toString())
    expect(() => allocator.allocate()).toThrow(/exhausted/)
  })
})
