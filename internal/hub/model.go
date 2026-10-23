package hub

import (
	"fmt"
	"strings"
	"time"

	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/protocol"
)

type RoundStatus string

const (
	RoundRunning RoundStatus = "RUNNING"
	RoundEnded   RoundStatus = "ENDED"
)

type Round struct {
	ID        string
	Status    RoundStatus
	CreatedAt time.Time
}

func NewRound(now time.Time) (Round, error) {
	id, err := protocol.NewID()
	if err != nil {
		return Round{}, err
	}
	return Round{ID: id, Status: RoundRunning, CreatedAt: now}, nil
}

type ParticipantState string

const (
	ParticipantOnline  ParticipantState = "ONLINE"
	ParticipantOffline ParticipantState = "OFFLINE"
	ParticipantRemoved ParticipantState = "REMOVED"
)

type Participant struct {
	ID              string
	Nickname        string
	ShortID         string
	Color           string
	State           ParticipantState
	ConnectionEpoch string
	OfflineSince    time.Time
	CreatedAt       time.Time
}

func NewParticipant(id, nickname, shortID, color string, now time.Time) Participant {
	return Participant{
		ID:        id,
		Nickname:  nickname,
		ShortID:   shortID,
		Color:     color,
		State:     ParticipantOnline,
		CreatedAt: now,
	}
}

func (p *Participant) MarkOnline(epoch string) {
	p.State = ParticipantOnline
	p.ConnectionEpoch = epoch
	p.OfflineSince = time.Time{}
}

func (p *Participant) MarkOffline(now time.Time) {
	if p.State == ParticipantRemoved {
		return
	}
	p.State = ParticipantOffline
	p.ConnectionEpoch = ""
	p.OfflineSince = now
}

func (p *Participant) MarkRemoved() {
	p.State = ParticipantRemoved
	p.ConnectionEpoch = ""
	p.OfflineSince = time.Time{}
}

func (p Participant) IsOnline() bool {
	return p.State == ParticipantOnline
}

type SessionState string

const (
	SessionInvited     SessionState = "INVITED"
	SessionNegotiating SessionState = "NEGOTIATING"
	SessionActive      SessionState = "ACTIVE"
	SessionClosed      SessionState = "CLOSED"
)

type Session struct {
	ID           string
	AID          string
	BID          string
	Mode         protocol.Mode
	State        SessionState
	CreatedAt    time.Time
	ExpiresAt    time.Time
	ClosedReason string

	APublicKey []byte
	BPublicKey []byte
	AReady     bool
	BReady     bool
}

func NewSession(id, aID, bID string, mode protocol.Mode, now, expires time.Time) (*Session, error) {
	if !protocol.ValidID(id) {
		return nil, fmt.Errorf("invalid session id")
	}
	if !protocol.ValidID(aID) || !protocol.ValidID(bID) {
		return nil, fmt.Errorf("invalid participant id")
	}
	if aID == bID {
		return nil, fmt.Errorf("session participants must differ")
	}
	if mode != protocol.ModePlaintext && mode != protocol.ModeEncrypted {
		return nil, fmt.Errorf("invalid session mode %q", mode)
	}
	return &Session{
		ID:        id,
		AID:       aID,
		BID:       bID,
		Mode:      mode,
		State:     SessionInvited,
		CreatedAt: now,
		ExpiresAt: expires,
	}, nil
}

func (s *Session) Accept(expires time.Time) {
	if s.State != SessionInvited {
		return
	}
	if s.Mode == protocol.ModePlaintext {
		s.State = SessionActive
		s.ExpiresAt = time.Time{}
		return
	}
	s.State = SessionNegotiating
	s.ExpiresAt = expires
}

func (s *Session) Activate() {
	if s.State != SessionNegotiating {
		return
	}
	s.State = SessionActive
	s.ExpiresAt = time.Time{}
}

func (s *Session) Close(reason string) {
	if s.State == SessionClosed {
		return
	}
	s.State = SessionClosed
	s.ClosedReason = reason
	s.ExpiresAt = time.Time{}
}

func (s *Session) IsOpen() bool {
	return s.State != SessionClosed
}

func (s *Session) Contains(participantID string) bool {
	return participantID == s.AID || participantID == s.BID
}

func (s *Session) Counterpart(participantID string) string {
	switch participantID {
	case s.AID:
		return s.BID
	case s.BID:
		return s.AID
	default:
		return ""
	}
}

func (s *Session) Direction(senderID string) string {
	switch senderID {
	case s.AID:
		return "a_to_b"
	case s.BID:
		return "b_to_a"
	default:
		return ""
	}
}

func PairKey(aID, bID string) string {
	if aID < bID {
		return aID + "|" + bID
	}
	return bID + "|" + aID
}

type ConnectionState string

const (
	ConnectionPending ConnectionState = "PENDING"
	ConnectionStudent ConnectionState = "STUDENT"
	ConnectionTeacher ConnectionState = "TEACHER"
	ConnectionClosed  ConnectionState = "CLOSED"
)

type Peer interface {
	Send(message any) error
	Close(reason string)
}

type Connection struct {
	Epoch         string
	ParticipantID string
	Admin         bool
	Subscription  string
	State         ConnectionState
	ConnectedAt   time.Time
	LastSeen      time.Time
	Peer          Peer
}

func NewConnection(epoch string, now time.Time) *Connection {
	return &Connection{
		Epoch:       epoch,
		State:       ConnectionPending,
		ConnectedAt: now,
		LastSeen:    now,
	}
}

func (c *Connection) Bind(participantID string) {
	if c.State != ConnectionPending {
		return
	}
	c.ParticipantID = participantID
	c.State = ConnectionStudent
}

func (c *Connection) AuthenticateTeacher() {
	if c.State != ConnectionPending {
		return
	}
	c.State = ConnectionTeacher
}

func (c *Connection) Close() {
	c.State = ConnectionClosed
}

func (c *Connection) IsOpen() bool {
	return c.State != ConnectionClosed
}

func ShortIDFor(id string) string {
	if len(id) < 4 {
		return ""
	}
	return strings.ToUpper(id[:4])
}

var ColorPalette = []string{
	"#2563EB",
	"#DC2626",
	"#16A34A",
	"#D97706",
	"#7C3AED",
	"#0891B2",
	"#DB2777",
	"#65A30D",
	"#EA580C",
	"#4F46E5",
	"#059669",
	"#9333EA",
}

func ColorForIndex(index int) string {
	if len(ColorPalette) == 0 {
		return ""
	}
	index %= len(ColorPalette)
	if index < 0 {
		index += len(ColorPalette)
	}
	return ColorPalette[index]
}
