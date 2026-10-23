export function toBase64(bytes: Uint8Array): string {
  let binary = ''
  const chunk = 0x8000
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk))
  }
  return btoa(binary)
}

export function fromBase64(text: string): Uint8Array {
  const binary = atob(text)
  const out = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) {
    out[i] = binary.charCodeAt(i)
  }
  return out
}

export function utf8Encode(text: string): Uint8Array {
  return new TextEncoder().encode(text)
}

export function utf8Decode(bytes: Uint8Array): string {
  return new TextDecoder('utf-8', { fatal: true }).decode(bytes)
}

export function secureRandomBytes(length: number): Uint8Array {
  const source = globalThis.crypto
  if (!source || typeof source.getRandomValues !== 'function') {
    throw new Error('secure random number generator is unavailable')
  }
  if (!Number.isInteger(length) || length < 0) {
    throw new Error('invalid random length')
  }
  const out = new Uint8Array(length)
  source.getRandomValues(out)
  return out
}
