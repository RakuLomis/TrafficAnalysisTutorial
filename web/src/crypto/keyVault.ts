import nacl from 'tweetnacl'
import { fromBase64, secureRandomBytes, toBase64, utf8Decode, utf8Encode } from './encoding'
import { type Direction, NonceAllocator, nonceCounter, seqMatchesNonce } from './nonce'

export type Role = 'a' | 'b'

export interface CreatedSession {
  sessionID: string
  role: Role
  direction: Direction
  publicKeyB64: string
  txPrefixB64: string
}

export interface KeyConfirmContext {
  roundID: string
  sessionID: string
  senderID: string
  recipientID: string
}

export interface ChatContext extends KeyConfirmContext {
  messageID: string
  text: string
}

export interface EncryptedPayload {
  seq: string
  nonceB64: string
  ciphertextB64: string
}

export interface DecryptedChat {
  text: string
  messageID: string
  senderID: string
  recipientID: string
  seq: string
}

export interface SessionStatus {
  confirmed: boolean
  peerPublicKeyB64?: string
  rxWatermark: bigint
}

export class CryptoError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'CryptoError'
  }
}

interface SessionEntry {
  role: Role
  direction: Direction
  publicKey: Uint8Array
  secretKey: Uint8Array
  allocator: NonceAllocator
  peerPublicKeyB64?: string
  peerPrefix?: Uint8Array
  sharedKey?: Uint8Array
  rxWatermark: bigint
  confirmed: boolean
  confirmBuilt: boolean
}

const MAX_BODY_BYTES = 4096
const MAX_CIPHERTEXT_BYTES = 32768

export class KeyVault {
  #sessions = new Map<string, SessionEntry>()

  create(sessionID: string, role: Role): CreatedSession {
    if (this.#sessions.has(sessionID)) {
      throw new CryptoError('session keys already exist')
    }
    if (role !== 'a' && role !== 'b') {
      throw new CryptoError('invalid role')
    }
    const pair = nacl.box.keyPair()
    const direction: Direction = role === 'a' ? 0 : 1
    const allocator = new NonceAllocator(direction, secureRandomBytes(15))
    this.#sessions.set(sessionID, {
      role,
      direction,
      publicKey: pair.publicKey,
      secretKey: pair.secretKey,
      allocator,
      rxWatermark: 0n,
      confirmed: false,
      confirmBuilt: false,
    })
    return {
      sessionID,
      role,
      direction,
      publicKeyB64: toBase64(pair.publicKey),
      txPrefixB64: toBase64(allocator.prefix),
    }
  }

  has(sessionID: string): boolean {
    return this.#sessions.has(sessionID)
  }

  sessionInfo(sessionID: string): CreatedSession {
    const entry = this.#require(sessionID)
    return {
      sessionID,
      role: entry.role,
      direction: entry.direction,
      publicKeyB64: toBase64(entry.publicKey),
      txPrefixB64: toBase64(entry.allocator.prefix),
    }
  }

  status(sessionID: string): SessionStatus {
    const entry = this.#require(sessionID)
    return {
      confirmed: entry.confirmed,
      peerPublicKeyB64: entry.peerPublicKeyB64,
      rxWatermark: entry.rxWatermark,
    }
  }

  setPeerPublicKey(sessionID: string, publicKeyB64: string): void {
    const entry = this.#require(sessionID)
    const key = decodeExact(publicKeyB64, 32, 'peer public key')
    if (entry.peerPublicKeyB64 !== undefined) {
      if (entry.peerPublicKeyB64 !== publicKeyB64) {
        throw new CryptoError('peer public key changed')
      }
      return
    }
    entry.peerPublicKeyB64 = publicKeyB64
    entry.sharedKey = nacl.box.before(key, entry.secretKey)
  }

  buildKeyConfirm(sessionID: string, context: KeyConfirmContext): EncryptedPayload {
    const entry = this.#requireShared(sessionID)
    if (entry.confirmBuilt) {
      throw new CryptoError('key confirm already built')
    }
    const { nonce, seq } = entry.allocator.allocate()
    const envelope = {
      v: 1,
      kind: 'key_confirm',
      round_id: context.roundID,
      session_id: context.sessionID,
      sender_id: context.senderID,
      recipient_id: context.recipientID,
      seq,
      sender_public_key_b64: toBase64(entry.publicKey),
      recipient_public_key_b64: entry.peerPublicKeyB64,
    }
    const ciphertext = nacl.box.after(utf8Encode(JSON.stringify(envelope)), nonce, entry.sharedKey)
    entry.confirmBuilt = true
    return { seq, nonceB64: toBase64(nonce), ciphertextB64: toBase64(ciphertext) }
  }

  verifyKeyConfirm(sessionID: string, context: KeyConfirmContext, payload: EncryptedPayload): void {
    const entry = this.#requireShared(sessionID)
    const nonce = decodeExact(payload.nonceB64, 24, 'nonce')
    const peerDirection = entry.role === 'a' ? 1 : 0
    if (nonce[0] !== peerDirection) {
      throw new CryptoError('invalid key-confirm direction')
    }
    if (payload.seq !== '0' || !seqMatchesNonce(payload.seq, nonce) || nonceCounter(nonce) !== 0n) {
      throw new CryptoError('invalid key-confirm seq')
    }
    const ciphertext = decodeExactBytes(payload.ciphertextB64, 'key-confirm ciphertext', 16, MAX_CIPHERTEXT_BYTES)
    const plaintext = nacl.box.open.after(ciphertext, nonce, entry.sharedKey)
    if (!plaintext) {
      throw new CryptoError('key-confirm authentication failed')
    }
    const envelope = parseEnvelope(plaintext, ['v', 'kind', 'round_id', 'session_id', 'sender_id', 'recipient_id', 'seq', 'sender_public_key_b64', 'recipient_public_key_b64'])
    if (
      envelope.v !== 1 ||
      envelope.kind !== 'key_confirm' ||
      envelope.round_id !== context.roundID ||
      envelope.session_id !== context.sessionID ||
      envelope.sender_id !== context.senderID ||
      envelope.recipient_id !== context.recipientID ||
      envelope.seq !== '0' ||
      envelope.sender_public_key_b64 !== entry.peerPublicKeyB64 ||
      envelope.recipient_public_key_b64 !== toBase64(entry.publicKey)
    ) {
      throw new CryptoError('key-confirm context mismatch')
    }
    const prefix = nonce.slice(1, 16)
    if (entry.confirmed) {
      if (!entry.peerPrefix || !equalBytes(entry.peerPrefix, prefix)) {
        throw new CryptoError('peer nonce prefix changed')
      }
      return
    }
    entry.peerPrefix = new Uint8Array(prefix)
    entry.rxWatermark = 1n
    entry.confirmed = true
  }

  encryptChat(sessionID: string, context: ChatContext): EncryptedPayload {
    const entry = this.#requireConfirmed(sessionID)
    const textBytes = utf8Encode(context.text)
    if (textBytes.length > MAX_BODY_BYTES) {
      throw new CryptoError('chat text exceeds limit')
    }
    const { nonce, seq } = entry.allocator.allocate()
    const envelope = {
      v: 1,
      kind: 'chat',
      round_id: context.roundID,
      session_id: context.sessionID,
      message_id: context.messageID,
      sender_id: context.senderID,
      recipient_id: context.recipientID,
      seq,
      text: context.text,
    }
    const ciphertext = nacl.box.after(utf8Encode(JSON.stringify(envelope)), nonce, entry.sharedKey)
    return { seq, nonceB64: toBase64(nonce), ciphertextB64: toBase64(ciphertext) }
  }

  decryptChat(sessionID: string, payload: EncryptedPayload, context: KeyConfirmContext & { messageID: string }): DecryptedChat {
    const entry = this.#requireConfirmed(sessionID)
    const nonce = decodeExact(payload.nonceB64, 24, 'nonce')
    const peerDirection = entry.role === 'a' ? 1 : 0
    if (nonce[0] !== peerDirection) {
      throw new CryptoError('invalid chat direction')
    }
    if (!entry.peerPrefix || !equalBytes(entry.peerPrefix, nonce.slice(1, 16))) {
      throw new CryptoError('unexpected nonce prefix')
    }
    if (!seqMatchesNonce(payload.seq, nonce)) {
      throw new CryptoError('seq does not match nonce counter')
    }
    const counter = nonceCounter(nonce)
    if (counter < entry.rxWatermark) {
      throw new CryptoError('replayed or reordered counter')
    }
    const ciphertext = decodeExactBytes(payload.ciphertextB64, 'ciphertext', 16, MAX_CIPHERTEXT_BYTES)
    const plaintext = nacl.box.open.after(ciphertext, nonce, entry.sharedKey)
    if (!plaintext) {
      throw new CryptoError('ciphertext authentication failed')
    }
    const envelope = parseEnvelope(plaintext, ['v', 'kind', 'round_id', 'session_id', 'message_id', 'sender_id', 'recipient_id', 'seq', 'text'])
    if (
      envelope.v !== 1 ||
      envelope.kind !== 'chat' ||
      envelope.round_id !== context.roundID ||
      envelope.session_id !== context.sessionID ||
      envelope.message_id !== context.messageID ||
      envelope.sender_id !== context.senderID ||
      envelope.recipient_id !== context.recipientID ||
      envelope.seq !== payload.seq ||
      typeof envelope.text !== 'string'
    ) {
      throw new CryptoError('chat envelope context mismatch')
    }
    const textBytes = utf8Encode(envelope.text)
    if (textBytes.length > MAX_BODY_BYTES) {
      throw new CryptoError('decrypted text exceeds limit')
    }
    entry.rxWatermark = counter + 1n
    return {
      text: envelope.text,
      messageID: envelope.message_id,
      senderID: envelope.sender_id,
      recipientID: envelope.recipient_id,
      seq: envelope.seq,
    }
  }

  destroy(sessionID: string): void {
    const entry = this.#sessions.get(sessionID)
    if (!entry) {
      return
    }
    entry.secretKey.fill(0)
    entry.sharedKey?.fill(0)
    entry.peerPrefix?.fill(0)
    entry.allocator.prefix.fill(0)
    this.#sessions.delete(sessionID)
  }

  destroyAll(): void {
    for (const sessionID of [...this.#sessions.keys()]) {
      this.destroy(sessionID)
    }
  }

  #require(sessionID: string): SessionEntry {
    const entry = this.#sessions.get(sessionID)
    if (!entry) {
      throw new CryptoError('missing session keys')
    }
    return entry
  }

  #requireShared(sessionID: string): SessionEntry & { sharedKey: Uint8Array; peerPublicKeyB64: string } {
    const entry = this.#require(sessionID)
    if (!entry.sharedKey || !entry.peerPublicKeyB64) {
      throw new CryptoError('peer public key is not set')
    }
    return entry as SessionEntry & { sharedKey: Uint8Array; peerPublicKeyB64: string }
  }

  #requireConfirmed(sessionID: string): SessionEntry & { sharedKey: Uint8Array; peerPublicKeyB64: string } {
    const entry = this.#requireShared(sessionID)
    if (!entry.confirmed) {
      throw new CryptoError('key confirm has not completed')
    }
    return entry
  }
}

function decodeExact(text: string, length: number, label: string): Uint8Array {
  return decodeExactBytes(text, label, length, length)
}

function decodeExactBytes(text: string, label: string, min: number, max: number): Uint8Array {
  let bytes: Uint8Array
  try {
    bytes = fromBase64(text)
  } catch {
    throw new CryptoError(`${label} is not valid base64`)
  }
  if (bytes.length < min || bytes.length > max) {
    throw new CryptoError(`${label} has invalid length`)
  }
  return bytes
}

function parseEnvelope(bytes: Uint8Array, allowed: string[]): Record<string, unknown> {
  let value: unknown
  try {
    value = JSON.parse(utf8Decode(bytes))
  } catch {
    throw new CryptoError('envelope is not valid UTF-8 JSON')
  }
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new CryptoError('envelope must be an object')
  }
  const record = value as Record<string, unknown>
  for (const key of Object.keys(record)) {
    if (!allowed.includes(key)) {
      throw new CryptoError(`unexpected envelope field ${key}`)
    }
  }
  return record
}

function equalBytes(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) {
    return false
  }
  let diff = 0
  for (let i = 0; i < a.length; i++) {
    diff |= a[i] ^ b[i]
  }
  return diff === 0
}

