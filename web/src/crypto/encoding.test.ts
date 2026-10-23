import { describe, expect, it } from 'vitest'
import { fromBase64, secureRandomBytes, toBase64, utf8Decode, utf8Encode } from './encoding'

describe('base64', () => {
  it('round-trips arbitrary bytes', () => {
    const bytes = new Uint8Array([0, 1, 2, 127, 128, 255, 42])
    expect(fromBase64(toBase64(bytes))).toEqual(bytes)
  })

  it('uses standard padded base64', () => {
    expect(toBase64(new Uint8Array([0, 0]))).toBe('AAA=')
    expect(toBase64(new Uint8Array(32))).toMatch(/=$/)
  })
})

describe('utf8', () => {
  it('round-trips Chinese and emoji', () => {
    for (const text of ['你好', '😀', 'line1\nline2', '']) {
      expect(utf8Decode(utf8Encode(text))).toBe(text)
    }
  })
})

describe('secureRandomBytes', () => {
  it('returns the requested length', () => {
    expect(secureRandomBytes(15)).toHaveLength(15)
    expect(secureRandomBytes(0)).toHaveLength(0)
  })

  it('rejects invalid lengths', () => {
    expect(() => secureRandomBytes(-1)).toThrow()
    expect(() => secureRandomBytes(1.5)).toThrow()
  })
})
