import { describe, expect, it } from 'vitest'
import { randomHex, utf8ByteLength } from './random'

describe('randomHex', () => {
  it('creates lowercase hex of the requested byte length', () => {
    const value = randomHex(16)
    expect(value).toMatch(/^[0-9a-f]{32}$/)
    expect(randomHex(2)).toMatch(/^[0-9a-f]{4}$/)
  })
})

describe('utf8ByteLength', () => {
  it('counts UTF-8 bytes, not characters', () => {
    expect(utf8ByteLength('abc')).toBe(3)
    expect(utf8ByteLength('你好')).toBe(6)
    expect(utf8ByteLength('😀')).toBe(4)
    expect(utf8ByteLength('')).toBe(0)
  })
})
