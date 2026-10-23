import { KeyVault, type EncryptedPayload } from '../crypto/keyVault'
import { randomHex } from '../shared/random'
import type { ChatDeliver, KeyConfirmMessage, KeyPublishMessage, SessionView } from '../shared/protocol'

export interface CryptoBasis {
  roundID: string
  selfID: string
  epoch: string
}

type SendFunction = (message: object) => boolean

export class StudentCrypto {
  #vault = new KeyVault()
  #send: SendFunction
  #sessions = new Map<string, SessionView>()
  #pendingPublish = new Map<string, KeyPublishMessage>()

  constructor(send: SendFunction) {
    this.#send = send
  }

  onSessionChanged(session: SessionView, basis: CryptoBasis): void {
    this.#sessions.set(session.session_id, session)
    if (session.state === 'CLOSED') {
      this.#vault.destroy(session.session_id)
      this.#pendingPublish.delete(session.session_id)
      return
    }
    if (session.state !== 'NEGOTIATING') {
      return
    }
    if (!this.#isMember(session, basis.selfID) || this.#vault.has(session.session_id)) {
      return
    }
    const role = session.a_id === basis.selfID ? 'a' : 'b'
    try {
      const info = this.#vault.create(session.session_id, role)
      this.#send({
        v: 1,
        type: 'key.publish',
        request_id: randomHex(),
        round_id: basis.roundID,
        connection_epoch: basis.epoch,
        session_id: session.session_id,
        public_key_b64: info.publicKeyB64,
      })
      const pending = this.#pendingPublish.get(session.session_id)
      if (pending) {
        this.#pendingPublish.delete(session.session_id)
        this.onKeyPublish(pending, basis)
      }
    } catch {
      this.#fail(session, basis)
    }
  }

  onKeyPublish(message: KeyPublishMessage, basis: CryptoBasis): void {
    const session = this.#sessions.get(message.session_id)
    if (!session) {
      this.#pendingPublish.set(message.session_id, message)
      return
    }
    if (!this.#vault.has(message.session_id)) {
      this.#pendingPublish.set(message.session_id, message)
      return
    }
    if (message.sender_id === basis.selfID) {
      return
    }
    try {
      this.#vault.setPeerPublicKey(message.session_id, message.public_key_b64)
      const confirm = this.#vault.buildKeyConfirm(message.session_id, {
        roundID: basis.roundID,
        sessionID: message.session_id,
        senderID: basis.selfID,
        recipientID: message.sender_id,
      })
      this.#send({
        v: 1,
        type: 'key.confirm',
        request_id: randomHex(),
        round_id: basis.roundID,
        connection_epoch: basis.epoch,
        session_id: message.session_id,
        seq: confirm.seq,
        nonce_b64: confirm.nonceB64,
        ciphertext_b64: confirm.ciphertextB64,
      })
    } catch {
      this.#fail(session, basis)
    }
  }

  onKeyConfirm(message: KeyConfirmMessage, basis: CryptoBasis): void {
    const session = this.#sessions.get(message.session_id)
    if (!session || message.sender_id === basis.selfID) {
      return
    }
    try {
      this.#vault.verifyKeyConfirm(message.session_id, {
        roundID: basis.roundID,
        sessionID: message.session_id,
        senderID: message.sender_id,
        recipientID: basis.selfID,
      }, {
        seq: message.seq,
        nonceB64: message.nonce_b64,
        ciphertextB64: message.ciphertext_b64,
      })
      this.#send({
        v: 1,
        type: 'key.ready',
        request_id: randomHex(),
        round_id: basis.roundID,
        connection_epoch: basis.epoch,
        session_id: message.session_id,
        status: 'ready',
      })
    } catch {
      this.#fail(session, basis)
    }
  }

  encryptChat(session: SessionView, basis: CryptoBasis, messageID: string, text: string): EncryptedPayload {
    const peerID = this.#peerID(session, basis.selfID)
    if (!peerID) {
      throw new Error('not a session member')
    }
    return this.#vault.encryptChat(session.session_id, {
      roundID: basis.roundID,
      sessionID: session.session_id,
      senderID: basis.selfID,
      recipientID: peerID,
      messageID,
      text,
    })
  }

  decryptChat(message: ChatDeliver, basis: CryptoBasis): string {
    if (!message.nonce_b64 || !message.ciphertext_b64) {
      throw new Error('encrypted message is missing cipher fields')
    }
    return this.#vault.decryptChat(message.session_id, {
      seq: message.seq,
      nonceB64: message.nonce_b64,
      ciphertextB64: message.ciphertext_b64,
    }, {
      roundID: basis.roundID,
      sessionID: message.session_id,
      senderID: message.sender_id,
      recipientID: basis.selfID,
      messageID: message.message_id,
    }).text
  }

  destroy(sessionID: string): void {
    this.#vault.destroy(sessionID)
    this.#sessions.delete(sessionID)
    this.#pendingPublish.delete(sessionID)
  }

  destroyAll(): void {
    this.#vault.destroyAll()
    this.#sessions.clear()
    this.#pendingPublish.clear()
  }

  #fail(session: SessionView, basis: CryptoBasis): void {
    this.#send({
      v: 1,
      type: 'key.ready',
      request_id: randomHex(),
      round_id: basis.roundID,
      connection_epoch: basis.epoch,
      session_id: session.session_id,
      status: 'failed',
    })
    this.destroy(session.session_id)
  }

  #peerID(session: SessionView, selfID: string): string | null {
    if (session.a_id === selfID) {
      return session.b_id
    }
    if (session.b_id === selfID) {
      return session.a_id
    }
    return null
  }

  #isMember(session: SessionView, selfID: string): boolean {
    return session.a_id === selfID || session.b_id === selfID
  }
}
