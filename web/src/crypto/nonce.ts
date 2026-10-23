import { secureRandomBytes } from './encoding'

export type Direction = 0 | 1

export const NONCE_LENGTH = 24
export const NONCE_PREFIX_LENGTH = 15
export const MAX_COUNTER = (1n << 64n) - 1n

export function buildNonce(direction: Direction, prefix: Uint8Array, counter: bigint): Uint8Array {
  if (direction !== 0 && direction !== 1) {
    throw new Error('invalid nonce direction')
  }
  if (prefix.length !== NONCE_PREFIX_LENGTH) {
    throw new Error('nonce prefix must be 15 bytes')
  }
  if (counter < 0n || counter > MAX_COUNTER) {
    throw new Error('nonce counter out of range')
  }
  const nonce = new Uint8Array(NONCE_LENGTH)
  nonce[0] = direction
  nonce.set(prefix, 1)
  let value = counter
  for (let i = NONCE_LENGTH - 1; i >= 16; i--) {
    nonce[i] = Number(value & 0xffn)
    value >>= 8n
  }
  return nonce
}

export function nonceCounter(nonce: Uint8Array): bigint {
  if (nonce.length !== NONCE_LENGTH) {
    throw new Error('nonce must be 24 bytes')
  }
  let value = 0n
  for (let i = 16; i < NONCE_LENGTH; i++) {
    value = (value << 8n) | BigInt(nonce[i])
  }
  return value
}

export function seqFromNonce(nonce: Uint8Array): string {
  return nonceCounter(nonce).toString()
}

export function seqMatchesNonce(seq: string, nonce: Uint8Array): boolean {
  if (!/^(0|[1-9][0-9]*)$/.test(seq)) {
    return false
  }
  try {
    return BigInt(seq) === nonceCounter(nonce)
  } catch {
    return false
  }
}

export class NonceAllocator {
  readonly direction: Direction
  readonly prefix: Uint8Array
  #counter: bigint
  #exhausted = false

  constructor(direction: Direction, prefix?: Uint8Array, start = 0n) {
    if (direction !== 0 && direction !== 1) {
      throw new Error('invalid nonce direction')
    }
    if (start < 0n || start > MAX_COUNTER) {
      throw new Error('invalid nonce start')
    }
    const source = prefix ?? secureRandomBytes(NONCE_PREFIX_LENGTH)
    if (source.length !== NONCE_PREFIX_LENGTH) {
      throw new Error('nonce prefix must be 15 bytes')
    }
    this.direction = direction
    this.prefix = new Uint8Array(source)
    this.#counter = start
  }

  get counter(): bigint {
    return this.#counter
  }

  allocate(): { nonce: Uint8Array; seq: string } {
    if (this.#exhausted) {
      throw new Error('nonce counter exhausted')
    }
    const counter = this.#counter
    if (counter > MAX_COUNTER) {
      this.#exhausted = true
      throw new Error('nonce counter exhausted')
    }
    const nonce = buildNonce(this.direction, this.prefix, counter)
    if (counter === MAX_COUNTER) {
      this.#exhausted = true
    } else {
      this.#counter = counter + 1n
    }
    return { nonce, seq: counter.toString() }
  }
}
