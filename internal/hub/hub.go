package hub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/config"
	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/protocol"
)

var (
	ErrHubClosed  = errors.New("hub closed")
	ErrNilCommand = errors.New("nil command")
)

type Hub struct {
	cfg    config.Config
	logger *slog.Logger
	now    func() time.Time

	commands chan Command
	done     chan struct{}
	stopOnce sync.Once

	state hubState
}

type hubState struct {
	round           Round
	participants    map[string]*Participant
	sessions        map[string]*Session
	connections     map[string]*Connection
	topologyVersion uint64
}

type Command interface {
	apply(*Hub)
}

type ConnectCommand struct {
	Conn  *Connection
	Reply chan error
}

func (c ConnectCommand) apply(h *Hub) {
	replyError(c.Reply, h.connect(c.Conn))
}

type DisconnectCommand struct {
	Epoch string
	Reply chan error
}

func (c DisconnectCommand) apply(h *Hub) {
	replyError(c.Reply, h.disconnect(c.Epoch))
}

type TouchCommand struct {
	Epoch string
	At    time.Time
}

func (c TouchCommand) apply(h *Hub) {
	h.touch(c.Epoch, c.At)
}

type snapshotCommand struct {
	reply chan Snapshot
}

func (c snapshotCommand) apply(h *Hub) {
	snapshot := h.snapshot()
	select {
	case c.reply <- snapshot:
	default:
	}
}

type Snapshot struct {
	Round           Round
	TopologyVersion uint64
	Participants    []Participant
	Sessions        []Session
	Connections     []ConnectionView
}

type ConnectionView struct {
	Epoch         string
	ParticipantID string
	State         ConnectionState
	ConnectedAt   time.Time
	LastSeen      time.Time
}

func New(cfg config.Config, logger *slog.Logger) (*Hub, error) {
	return newHub(cfg, logger, time.Now)
}

func newHub(cfg config.Config, logger *slog.Logger, now func() time.Time) (*Hub, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	round, err := NewRound(now())
	if err != nil {
		return nil, fmt.Errorf("create initial round: %w", err)
	}
	return &Hub{
		cfg:      cfg,
		logger:   logger,
		now:      now,
		commands: make(chan Command, 256),
		done:     make(chan struct{}),
		state: hubState{
			round:        round,
			participants: make(map[string]*Participant),
			sessions:     make(map[string]*Session),
			connections:  make(map[string]*Connection),
		},
	}, nil
}

func (h *Hub) Run(ctx context.Context) {
	defer h.markDone()
	for {
		select {
		case <-ctx.Done():
			h.shutdown()
			return
		case cmd := <-h.commands:
			if cmd != nil {
				cmd.apply(h)
			}
		}
	}
}

func (h *Hub) Done() <-chan struct{} {
	return h.done
}

func (h *Hub) Submit(ctx context.Context, cmd Command) error {
	if cmd == nil {
		return ErrNilCommand
	}
	select {
	case <-h.done:
		return ErrHubClosed
	default:
	}
	select {
	case h.commands <- cmd:
		return nil
	case <-h.done:
		return ErrHubClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Hub) Snapshot(ctx context.Context) (Snapshot, error) {
	reply := make(chan Snapshot, 1)
	if err := h.Submit(ctx, snapshotCommand{reply: reply}); err != nil {
		return Snapshot{}, err
	}
	select {
	case snapshot := <-reply:
		return snapshot, nil
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	case <-h.done:
		return Snapshot{}, ErrHubClosed
	}
}

func (h *Hub) connect(conn *Connection) error {
	if conn == nil {
		return errors.New("nil connection")
	}
	if !protocol.ValidID(conn.Epoch) {
		return errors.New("invalid connection epoch")
	}
	if conn.State == ConnectionClosed {
		return errors.New("connection is closed")
	}
	if _, exists := h.state.connections[conn.Epoch]; exists {
		return fmt.Errorf("connection epoch already registered")
	}
	stored := *conn
	if stored.ConnectedAt.IsZero() {
		stored.ConnectedAt = h.now()
	}
	if stored.LastSeen.IsZero() {
		stored.LastSeen = stored.ConnectedAt
	}
	connCopy := &stored
	h.state.connections[connCopy.Epoch] = connCopy
	h.state.topologyVersion++
	if connCopy.ParticipantID != "" {
		if participant, ok := h.state.participants[connCopy.ParticipantID]; ok {
			participant.MarkOnline(connCopy.Epoch)
		}
	}
	return nil
}

func (h *Hub) disconnect(epoch string) error {
	conn, ok := h.state.connections[epoch]
	if !ok {
		return nil
	}
	delete(h.state.connections, epoch)
	conn.Close()
	h.state.topologyVersion++
	if conn.ParticipantID != "" {
		if participant, ok := h.state.participants[conn.ParticipantID]; ok && participant.ConnectionEpoch == epoch {
			participant.MarkOffline(h.now())
		}
	}
	return nil
}

func (h *Hub) touch(epoch string, at time.Time) {
	conn, ok := h.state.connections[epoch]
	if !ok || !conn.IsOpen() {
		return
	}
	if at.IsZero() {
		at = h.now()
	}
	conn.LastSeen = at
}

func (h *Hub) shutdown() {
	h.markDone()
	for _, conn := range h.state.connections {
		conn.Close()
		if conn.Peer != nil {
			conn.Peer.Close("server_shutdown")
		}
	}
	h.state.connections = make(map[string]*Connection)
	h.logger.Debug("hub stopped", "round_id", h.state.round.ID)
}

func (h *Hub) snapshot() Snapshot {
	participants := make([]Participant, 0, len(h.state.participants))
	for _, participant := range h.state.participants {
		participants = append(participants, *participant)
	}
	sort.Slice(participants, func(i, j int) bool { return participants[i].ID < participants[j].ID })

	sessions := make([]Session, 0, len(h.state.sessions))
	for _, session := range h.state.sessions {
		sessions = append(sessions, cloneSession(session))
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].ID < sessions[j].ID })

	connections := make([]ConnectionView, 0, len(h.state.connections))
	for _, conn := range h.state.connections {
		connections = append(connections, ConnectionView{
			Epoch:         conn.Epoch,
			ParticipantID: conn.ParticipantID,
			State:         conn.State,
			ConnectedAt:   conn.ConnectedAt,
			LastSeen:      conn.LastSeen,
		})
	}
	sort.Slice(connections, func(i, j int) bool { return connections[i].Epoch < connections[j].Epoch })

	return Snapshot{
		Round:           h.state.round,
		TopologyVersion: h.state.topologyVersion,
		Participants:    participants,
		Sessions:        sessions,
		Connections:     connections,
	}
}

func cloneSession(session *Session) Session {
	copy := *session
	copy.APublicKey = append([]byte(nil), session.APublicKey...)
	copy.BPublicKey = append([]byte(nil), session.BPublicKey...)
	return copy
}

func (h *Hub) markDone() {
	h.stopOnce.Do(func() {
		close(h.done)
	})
}

func replyError(reply chan error, err error) {
	if reply == nil {
		return
	}
	select {
	case reply <- err:
	default:
	}
}
