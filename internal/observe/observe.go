package observe

import "github.com/RakuLomis/TrafficAnalysisTutorial/internal/protocol"

type Store struct {
	maxSessionCount int
	maxSessionBytes int64
	maxGlobalBytes  int64

	sessions    map[string]*sessionState
	globalBytes int64
}

type sessionState struct {
	records       []protocol.ObserverRecord
	stats         protocol.ObserverStats
	mode          string
	bytes         int64
	truncated     bool
	lastMono      int64
	hasLast       bool
	lastDirection string
}

func New(maxSessionCount, maxSessionBytes, maxGlobalBytes int) *Store {
	if maxSessionCount < 1 {
		maxSessionCount = 200
	}
	if maxSessionBytes < 1 {
		maxSessionBytes = 1 << 20
	}
	if maxGlobalBytes < maxSessionBytes {
		maxGlobalBytes = 32 << 20
	}
	return &Store{
		maxSessionCount: maxSessionCount,
		maxSessionBytes: int64(maxSessionBytes),
		maxGlobalBytes:  int64(maxGlobalBytes),
		sessions:        make(map[string]*sessionState),
	}
}

func (s *Store) Add(record protocol.ObserverRecord) protocol.ObserverRecord {
	state := s.sessions[record.SessionID]
	if state == nil {
		state = &sessionState{}
		s.sessions[record.SessionID] = state
	}
	record.IntervalMS = nil
	if state.hasLast && record.MonoMS >= state.lastMono {
		interval := record.MonoMS - state.lastMono
		record.IntervalMS = &interval
	}
	if state.lastDirection != "" && state.lastDirection != record.Direction {
		state.stats.DirectionSwitches++
	}
	if record.ReceiptStatus == "" {
		record.ReceiptStatus = "pending"
	}
	state.stats.MessageCount++
	state.stats.PayloadBytesTotal += int64(record.PayloadBytes)
	state.stats.WSMessageBytesTotal += int64(record.WSMessageBytes)
	state.stats.LastDirection = record.Direction
	if state.stats.FirstAtUTC == "" {
		state.stats.FirstAtUTC = record.AtUTC
	}
	state.stats.LastAtUTC = record.AtUTC
	state.mode = record.Mode
	state.lastMono = record.MonoMS
	state.hasLast = true
	state.lastDirection = record.Direction
	state.records = append(state.records, record)
	state.bytes += int64(record.PayloadBytes)
	s.globalBytes += int64(record.PayloadBytes)

	for len(state.records) > s.maxSessionCount || state.bytes > s.maxSessionBytes {
		s.removeFirst(state)
	}
	for s.globalBytes > s.maxGlobalBytes {
		victim := s.oldestSession()
		if victim == nil {
			break
		}
		s.removeFirst(victim)
	}
	return record
}

func (s *Store) UpdateReceipt(sessionID, messageID, status string) (protocol.ObserverRecord, bool) {
	state := s.sessions[sessionID]
	if state == nil {
		return protocol.ObserverRecord{}, false
	}
	for index := range state.records {
		record := &state.records[index]
		if record.MessageID != messageID {
			continue
		}
		if record.ReceiptStatus == status {
			return *record, false
		}
		adjustReceiptCount(&state.stats, record.ReceiptStatus, -1)
		record.ReceiptStatus = status
		adjustReceiptCount(&state.stats, status, 1)
		return *record, true
	}
	return protocol.ObserverRecord{}, false
}

func (s *Store) Snapshot(sessionID string) (protocol.ObserverSnapshot, bool) {
	state := s.sessions[sessionID]
	if state == nil {
		return protocol.ObserverSnapshot{}, false
	}
	records := make([]protocol.ObserverRecord, len(state.records))
	copy(records, state.records)
	return protocol.ObserverSnapshot{
		SessionID: sessionID,
		Mode:      state.mode,
		Stats:     state.stats,
		Truncated: state.truncated,
		Messages:  records,
	}, true
}

func (s *Store) Reset() {
	s.sessions = make(map[string]*sessionState)
	s.globalBytes = 0
}

func (s *Store) removeFirst(state *sessionState) {
	if len(state.records) == 0 {
		return
	}
	removed := state.records[0]
	state.records = state.records[1:]
	state.bytes -= int64(removed.PayloadBytes)
	s.globalBytes -= int64(removed.PayloadBytes)
	state.truncated = true
}

func (s *Store) oldestSession() *sessionState {
	var victim *sessionState
	var oldest int64
	for _, state := range s.sessions {
		if len(state.records) == 0 {
			continue
		}
		first := state.records[0].MonoMS
		if victim == nil || first < oldest {
			victim = state
			oldest = first
		}
	}
	return victim
}

func adjustReceiptCount(stats *protocol.ObserverStats, status string, delta int) {
	switch status {
	case "confirmed":
		stats.ConfirmedCount += delta
	case "unconfirmed":
		stats.UnconfirmedCount += delta
	case "failed":
		stats.FailedCount += delta
	}
}
