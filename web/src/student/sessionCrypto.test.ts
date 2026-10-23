import { describe, expect, it } from 'vitest'
import { StudentCrypto } from './sessionCrypto'
import type { ChatDeliver, KeyConfirmMessage, KeyPublishMessage, SessionView } from '../shared/protocol'

const roundID = 'r'.repeat(32)
const aID = 'a'.repeat(32)
const bID = 'b'.repeat(32)
const session: SessionView = {
  session_id: 's'.repeat(32),
  a_id: aID,
  b_id: bID,
  mode: 'encrypted',
  state: 'NEGOTIATING',
}

const basisA = { roundID, selfID: aID, epoch: 'e'.repeat(32) }
const basisB = { roundID, selfID: bID, epoch: 'f'.repeat(32) }

function published(sent: object[], senderID: string): KeyPublishMessage {
  const message = sent.find((item) => (item as { type?: string }).type === 'key.publish')
  if (!message) {
    throw new Error('missing key.publish')
  }
  return { ...(message as Omit<KeyPublishMessage, 'sender_id'>), sender_id: senderID }
}

function confirmed(sent: object[], senderID: string): KeyConfirmMessage {
  const message = sent.find((item) => (item as { type?: string }).type === 'key.confirm')
  if (!message) {
    throw new Error('missing key.confirm')
  }
  return { ...(message as Omit<KeyConfirmMessage, 'sender_id'>), sender_id: senderID }
}

describe('StudentCrypto', () => {
  it('orchestrates public keys, confirms, ready and chat', () => {
    const sentA: object[] = []
    const sentB: object[] = []
    const alice = new StudentCrypto((message) => {
      sentA.push(message)
      return true
    })
    const bob = new StudentCrypto((message) => {
      sentB.push(message)
      return true
    })

    alice.onSessionChanged(session, basisA)
    bob.onSessionChanged(session, basisB)
    const aliceKey = published(sentA, aID)
    const bobKey = published(sentB, bID)

    alice.onKeyPublish(bobKey, basisA)
    bob.onKeyPublish(aliceKey, basisB)
    const aliceConfirm = confirmed(sentA, aID)
    const bobConfirm = confirmed(sentB, bID)

    alice.onKeyConfirm(bobConfirm, basisA)
    bob.onKeyConfirm(aliceConfirm, basisB)
    expect(sentA.some((message) => (message as { type?: string; status?: string }).type === 'key.ready' && (message as { status?: string }).status === 'ready')).toBe(true)
    expect(sentB.some((message) => (message as { type?: string; status?: string }).type === 'key.ready' && (message as { status?: string }).status === 'ready')).toBe(true)

    const messageID = 'm'.repeat(32)
    const payload = alice.encryptChat(session, basisA, messageID, '你好')
    const deliver: ChatDeliver = {
      v: 1,
      type: 'chat.deliver',
      round_id: roundID,
      connection_epoch: basisB.epoch,
      session_id: session.session_id,
      message_id: messageID,
      sender_id: aID,
      seq: payload.seq,
      nonce_b64: payload.nonceB64,
      ciphertext_b64: payload.ciphertextB64,
      sent_at_utc: '2026-10-23T03:00:00.000Z',
    }
    expect(bob.decryptChat(deliver, basisB)).toBe('你好')
  })

  it('reports key.ready failed and destroys on bad confirm', () => {
    const sentA: object[] = []
    const sentB: object[] = []
    const alice = new StudentCrypto((message) => {
      sentA.push(message)
      return true
    })
    const bob = new StudentCrypto((message) => {
      sentB.push(message)
      return true
    })
    alice.onSessionChanged(session, basisA)
    bob.onSessionChanged(session, basisB)
    alice.onKeyPublish(published(sentB, bID), basisA)
    const confirm = confirmed(sentA, aID)
    bob.onKeyPublish(published(sentA, aID), basisB)
    const bytes = Uint8Array.from(atob(confirm.ciphertext_b64), (char) => char.charCodeAt(0))
    bytes[0] ^= 0xff
    const tampered: KeyConfirmMessage = {
      ...confirm,
      ciphertext_b64: btoa(String.fromCharCode(...bytes)),
    }
    bob.onKeyConfirm(tampered, basisB)
    const failed = sentB.find((message) => (message as { type?: string; status?: string }).type === 'key.ready')
    expect(failed).toMatchObject({ status: 'failed' })
    expect(() => bob.encryptChat(session, basisB, 'm'.repeat(32), 'x')).toThrow(/missing session keys/)
  })
})
