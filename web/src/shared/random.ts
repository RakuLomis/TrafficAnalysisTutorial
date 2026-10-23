import { secureRandomBytes } from '../crypto/encoding'

export function randomHex(byteLength = 16): string {
  const bytes = secureRandomBytes(byteLength)
  let out = ''
  for (const byte of bytes) {
    out += byte.toString(16).padStart(2, '0')
  }
  return out
}

export function utf8ByteLength(text: string): number {
  return new TextEncoder().encode(text).length
}
