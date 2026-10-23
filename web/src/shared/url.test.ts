import { describe, expect, it } from 'vitest'
import { buildStudentURL, isDisplayableHost, portFromAddress } from './url'

describe('isDisplayableHost', () => {
  it('rejects wildcard, loopback and localhost', () => {
    for (const host of ['', '0.0.0.0', 'localhost', '127.0.0.1', '::', '::1']) {
      expect(isDisplayableHost(host)).toBe(false)
    }
  })

  it('accepts a LAN IPv4 address', () => {
    expect(isDisplayableHost('192.168.5.5')).toBe(true)
  })
})

describe('buildStudentURL', () => {
  it('builds an http URL from a LAN IPv4 and port', () => {
    expect(buildStudentURL('192.168.5.5', 8080)).toBe('http://192.168.5.5:8080/')
  })

  it('builds an http URL from a numeric string port', () => {
    expect(buildStudentURL('10.0.0.8', '8090')).toBe('http://10.0.0.8:8090/')
  })

  it('brackets IPv6 hosts', () => {
    expect(buildStudentURL('2001:db8::1', 8080)).toBe('http://[2001:db8::1]:8080/')
  })

  it('rejects non-displayable hosts', () => {
    expect(() => buildStudentURL('0.0.0.0', 8080)).toThrow()
    expect(() => buildStudentURL('localhost', 8080)).toThrow()
    expect(() => buildStudentURL('127.0.0.1', 8080)).toThrow()
  })

  it('rejects invalid ports', () => {
    for (const port of [0, 65536, -1, 1.5, 'abc']) {
      expect(() => buildStudentURL('192.168.5.5', port)).toThrow()
    }
  })
})

describe('portFromAddress', () => {
  it('extracts a valid port', () => {
    expect(portFromAddress('0.0.0.0:8080')).toBe(8080)
    expect(portFromAddress('127.0.0.1:8081')).toBe(8081)
  })

  it('returns null for addresses without a valid port', () => {
    expect(portFromAddress('192.168.5.5')).toBeNull()
    expect(portFromAddress('host:0')).toBeNull()
    expect(portFromAddress('host:99999')).toBeNull()
  })
})
