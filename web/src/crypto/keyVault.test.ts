import { describe, expect, it } from 'vitest'
import { KeyVault } from './keyVault'
import { fromBase64, toBase64 } from './encoding'

const roundID = 'r'.repeat(32)
const sessionID = 's'.repeat(32)
const aliceID = 'a'.repeat(32)
const bobID = 'b'.repeat(32)

function setup() {
  const alice = new KeyVault()
  const bob = new KeyVault()
  const aliceInfo = alice.create(sessionID, 'a')
  const bobInfo = bob.create(sessionID, 'b')
  alice.setPeerPublicKey(sessionID, bobInfo.publicKeyB64)
  bob.setPeerPublicKey(sessionID, aliceInfo.publicKeyB64)

  const aliceConfirm = alice.buildKeyConfirm(sessionID, {
    roundID,
    sessionID,
    senderID: aliceID,
    recipientID: bobID,
  })
  bob.verifyKeyConfirm(sessionID, {
    roundID,
    sessionID,
    senderID: aliceID,
    recipientID: bobID,
  }, aliceConfirm)

  const bobConfirm = bob.buildKeyConfirm(sessionID, {
    roundID,
    sessionID,
    senderID: bobID,
    recipientID: aliceID,
  })
  alice.verifyKeyConfirm(sessionID, {
    roundID,
    sessionID,
    senderID: bobID,
    recipientID: aliceID,
  }, bobConfirm)
  return { alice, bob, aliceInfo, bobInfo }
}

describe('KeyVault', () => {
  it('creates independent per-session key pairs with public metadata only', () => {
    const vault = new KeyVault()
    const first = vault.create(sessionID, 'a')
    const otherID = 't'.repeat(32)
    const second = vault.create(otherID, 'b')
    expect(fromBase64(first.publicKeyB64)).toHaveLength(32)
    expect(fromBase64(second.publicKeyB64)).toHaveLength(32)
    expect(first.publicKeyB64).not.toBe(second.publicKeyB64)
    expect(first.direction).toBe(0)
    expect(second.direction).toBe(1)
    expect(JSON.stringify(first)).not.toContain('secret')
    expect(JSON.stringify(vault)).toBe('{}')
  })

  it('rejects duplicate session creation and invalid roles', () => {
    const vault = new KeyVault()
    vault.create(sessionID, 'a')
    expect(() => vault.create(sessionID, 'b')).toThrow(/already exist/)
    expect(() => vault.create('x'.repeat(32), 'c' as 'a')).toThrow()
  })

  it('rejects peer public key replacement', () => {
    const alice = new KeyVault()
    const bob = new KeyVault()
    alice.create(sessionID, 'a')
    const bobInfo = bob.create(sessionID, 'b')
    const other = bob.create('o'.repeat(32), 'b')
    alice.setPeerPublicKey(sessionID, bobInfo.publicKeyB64)
    expect(() => alice.setPeerPublicKey(sessionID, other.publicKeyB64)).toThrow(/changed/)
  })

  it('encrypts and decrypts chat in both directions', () => {
    const { alice, bob } = setup()
    const context = { roundID, sessionID, senderID: aliceID, recipientID: bobID, messageID: 'm'.repeat(32) }
    const payload = alice.encryptChat(sessionID, { ...context, text: '你好 😀' })
    expect(payload.seq).toBe('1')
    const decrypted = bob.decryptChat(sessionID, payload, context)
    expect(decrypted.text).toBe('你好 😀')
    expect(decrypted.senderID).toBe(aliceID)
    expect(decrypted.recipientID).toBe(bobID)

    const replyContext = { roundID, sessionID, senderID: bobID, recipientID: aliceID, messageID: 'n'.repeat(32) }
    const reply = bob.encryptChat(sessionID, { ...replyContext, text: 'reply' })
    expect(reply.seq).toBe('1')
    expect(alice.decryptChat(sessionID, reply, replyContext).text).toBe('reply')
  })

  it('rejects tampered ciphertext without advancing the watermark', () => {
    const { alice, bob } = setup()
    const context = { roundID, sessionID, senderID: aliceID, recipientID: bobID, messageID: 'm'.repeat(32) }
    const payload = alice.encryptChat(sessionID, { ...context, text: 'tamper' })
    const bytes = fromBase64(payload.ciphertextB64)
    bytes[bytes.length - 1] ^= 0xff
    expect(() => bob.decryptChat(sessionID, { ...payload, ciphertextB64: toBase64(bytes) }, context)).toThrow(/authentication/)
    expect(bob.status(sessionID).rxWatermark).toBe(1n)

    expect(bob.decryptChat(sessionID, payload, context).text).toBe('tamper')
  })

  it('rejects replay, wrong direction, wrong prefix and seq mismatch', () => {
    const { alice, bob } = setup()
    const context = { roundID, sessionID, senderID: aliceID, recipientID: bobID, messageID: 'm'.repeat(32) }
    const payload = alice.encryptChat(sessionID, { ...context, text: 'once' })
    bob.decryptChat(sessionID, payload, context)
    expect(() => bob.decryptChat(sessionID, payload, context)).toThrow(/replayed/)

    const next = alice.encryptChat(sessionID, { ...context, messageID: 'n'.repeat(32), text: 'next' })
    const nonce = fromBase64(next.nonceB64)
    nonce[0] = 1
    expect(() => bob.decryptChat(sessionID, { ...next, nonceB64: toBase64(nonce) }, { ...context, messageID: 'n'.repeat(32) })).toThrow(/direction/)

    const nonce2 = fromBase64(next.nonceB64)
    nonce2[1] ^= 0xff
    expect(() => bob.decryptChat(sessionID, { ...next, nonceB64: toBase64(nonce2) }, { ...context, messageID: 'n'.repeat(32) })).toThrow(/prefix/)

    const nonce3 = fromBase64(next.nonceB64)
    nonce3[23] += 1
    expect(() => bob.decryptChat(sessionID, { ...next, nonceB64: toBase64(nonce3) }, { ...context, messageID: 'n'.repeat(32) })).toThrow(/seq/)
  })

  it('rejects context mismatch without advancing the watermark', () => {
    const { alice, bob } = setup()
    const payload = alice.encryptChat(sessionID, {
      roundID,
      sessionID,
      senderID: aliceID,
      recipientID: 'x'.repeat(32),
      messageID: 'm'.repeat(32),
      text: 'wrong recipient',
    })
    expect(() => bob.decryptChat(sessionID, payload, {
      roundID,
      sessionID,
      senderID: aliceID,
      recipientID: bobID,
      messageID: 'm'.repeat(32),
    })).toThrow(/context/)
    expect(bob.status(sessionID).rxWatermark).toBe(1n)
  })

  it('enforces the 4096-byte plaintext limit', () => {
    const { alice } = setup()
    expect(() => alice.encryptChat(sessionID, {
      roundID,
      sessionID,
      senderID: aliceID,
      recipientID: bobID,
      messageID: 'm'.repeat(32),
      text: 'a'.repeat(4097),
    })).toThrow(/exceeds/)
  })

  it('rejects tampered key-confirm and context mismatch', () => {
    const alice = new KeyVault()
    const bob = new KeyVault()
    const aliceInfo = alice.create(sessionID, 'a')
    const bobInfo = bob.create(sessionID, 'b')
    alice.setPeerPublicKey(sessionID, bobInfo.publicKeyB64)
    bob.setPeerPublicKey(sessionID, aliceInfo.publicKeyB64)
    const context = { roundID, sessionID, senderID: aliceID, recipientID: bobID }
    const confirm = alice.buildKeyConfirm(sessionID, context)

    const bytes = fromBase64(confirm.ciphertextB64)
    bytes[0] ^= 0xff
    expect(() => bob.verifyKeyConfirm(sessionID, context, { ...confirm, ciphertextB64: toBase64(bytes) })).toThrow(/authentication/)

    expect(() => bob.verifyKeyConfirm(sessionID, { ...context, recipientID: 'x'.repeat(32) }, confirm)).toThrow(/context/)
  })

  it('destroys keys idempotently and blocks later use', () => {
    const { alice } = setup()
    alice.destroy(sessionID)
    expect(alice.has(sessionID)).toBe(false)
    expect(() => alice.encryptChat(sessionID, {
      roundID,
      sessionID,
      senderID: aliceID,
      recipientID: bobID,
      messageID: 'm'.repeat(32),
      text: 'after destroy',
    })).toThrow(/missing session keys/)
    alice.destroy(sessionID)
    alice.destroyAll()
  })
})

