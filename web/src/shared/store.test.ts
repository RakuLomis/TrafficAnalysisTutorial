import { describe, expect, it } from 'vitest'
import { ClassroomStore, type ChatMessage } from './store'
import type { Welcome } from './protocol'

const round = { id: 'r'.repeat(32), status: 'RUNNING' as const }

function welcome(): Welcome {
  return {
    v: 1,
    type: 'welcome',
    request_id: 'q'.repeat(32),
    participant_id: 'a'.repeat(32),
    nickname: '小明',
    short_id: 'AAAA',
    color: '#2563EB',
    resume_token: 't'.repeat(43),
    connection_epoch: 'e'.repeat(32),
    round,
    topology_version: 1,
    participants: [
      { id: 'a'.repeat(32), nickname: '小明', short_id: 'AAAA', color: '#2563EB', online: true },
      { id: 'b'.repeat(32), nickname: '小红', short_id: 'BBBB', color: '#DC2626', online: true },
    ],
    sessions: [],
  }
}

function message(id: string, text = 'hello'): ChatMessage {
  return {
    id,
    sessionID: 's'.repeat(32),
    senderID: 'a'.repeat(32),
    text,
    direction: 'out',
    status: 'SENDING',
    atUTC: '2026-10-23T03:00:00.000Z',
  }
}

describe('ClassroomStore', () => {
  it('rebuilds identity and topology from welcome', () => {
    const store = new ClassroomStore()
    store.applyWelcome(welcome())
    const state = store.getState()
    expect(state.identity?.nickname).toBe('小明')
    expect(state.participants.size).toBe(2)
    expect(state.topologyVersion).toBe(1)
    expect(state.connected).toBe(true)
  })

  it('ignores stale topology versions', () => {
    const store = new ClassroomStore()
    store.applyWelcome(welcome())
    store.applyTopology({
      v: 1,
      type: 'topology.changed',
      round_id: round.id,
      connection_epoch: 'e'.repeat(32),
      topology_version: 0,
      participants: [],
      sessions: [],
    })
    expect(store.getState().participants.size).toBe(2)
  })

  it('updates message status and caps per-session history at 200', () => {
    const store = new ClassroomStore()
    store.applyWelcome(welcome())
    for (let i = 0; i < 205; i++) {
      store.addMessage(message(String(i).padStart(32, '0'), `m${i}`))
    }
    expect(store.sessionMessages('s'.repeat(32))).toHaveLength(200)
    expect(store.sessionMessages('s'.repeat(32))[0].text).toBe('m5')
    store.updateMessageStatus('s'.repeat(32), String(204).padStart(32, '0'), 'CONFIRMED')
    expect(store.sessionMessages('s'.repeat(32)).at(-1)?.status).toBe('CONFIRMED')
  })

  it('keeps total cache under 3 MiB', () => {
    const store = new ClassroomStore()
    store.applyWelcome(welcome())
    const large = 'x'.repeat(1024 * 1024)
    for (let i = 0; i < 4; i++) {
      store.addMessage(message(String(i).padStart(32, '0'), large))
    }
    expect(store.totalMessageBytes()).toBeLessThanOrEqual(3 * 1024 * 1024)
    expect(store.sessionMessages('s'.repeat(32)).length).toBeLessThan(4)
  })

  it('resets all in-memory classroom state', () => {
    const store = new ClassroomStore()
    store.applyWelcome(welcome())
    store.addMessage(message('1'.repeat(32)))
    store.reset()
    const state = store.getState()
    expect(state.identity).toBeNull()
    expect(state.messages.size).toBe(0)
    expect(state.connected).toBe(false)
  })
})

