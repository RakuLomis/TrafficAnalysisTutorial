import { describe, expect, it } from 'vitest'
import { isPongPayload } from './pong'

describe('isPongPayload', () => {
  it('accepts a protocol v1 pong', () => {
    expect(isPongPayload('{"v":1,"type":"pong","server_time_utc":"2026-10-23T03:00:00Z"}')).toBe(true)
  })

  it('rejects other messages and invalid JSON', () => {
    expect(isPongPayload('{"v":1,"type":"ping"}')).toBe(false)
    expect(isPongPayload('{"v":2,"type":"pong"}')).toBe(false)
    expect(isPongPayload('not json')).toBe(false)
  })
})
