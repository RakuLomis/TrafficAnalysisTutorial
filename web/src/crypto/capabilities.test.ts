import { afterEach, describe, expect, it, vi } from 'vitest'
import { assertCryptoAvailable, checkCryptoCapabilities } from './capabilities'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('crypto capabilities', () => {
  it('reports secure random and nacl.box availability', () => {
    const capabilities = checkCryptoCapabilities()
    expect(capabilities.secureRandom).toBe(true)
    expect(capabilities.box).toBe(true)
    expect(capabilities.encryption).toBe(true)
    expect(() => assertCryptoAvailable()).not.toThrow()
  })

  it('fails closed when secure random is unavailable', () => {
    vi.stubGlobal('crypto', undefined)
    const capabilities = checkCryptoCapabilities()
    expect(capabilities.secureRandom).toBe(false)
    expect(capabilities.encryption).toBe(false)
    expect(() => assertCryptoAvailable()).toThrow(/unavailable/)
  })
})
