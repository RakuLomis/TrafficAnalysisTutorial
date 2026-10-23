package hub

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/config"
	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/protocol"
)

var (
	ErrHubClosed  = errors.New("hub closed")
	ErrNilCommand = errors.New("nil command")
)

type Options struct {
	AdminToken    string
	ServerVersion string
}

type Hub struct {
	cfg     config.Config
	options Options
	logger  *slog.Logger
	now     func() time.Time
	roundID atomic.Value

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
	resumeTokens    map[string]string
	shortIDs        map[string]string
	nextColor       int
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

type MessageCommand struct {
	Epoch   string
	Message protocol.Message
}

func (c MessageCommand) apply(h *Hub) {
	h.handleMessage(c.Epoch, c.Message)
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
	Admin         bool
	State         ConnectionState
	ConnectedAt   time.Time
	LastSeen      time.Time
}

func New(cfg config.Config, logger *slog.Logger) (*Hub, error) {
	return NewWithOptions(cfg, logger, Options{})
}

func NewWithOptions(cfg config.Config, logger *slog.Logger, options Options) (*Hub, error) {
	return newHubWithOptions(cfg, logger, time.Now, options)
}

func newHub(cfg config.Config, logger *slog.Logger, now func() time.Time) (*Hub, error) {
	return newHubWithOptions(cfg, logger, now, Options{})
}

func newHubWithOptions(cfg config.Config, logger *slog.Logger, now func() time.Time, options Options) (*Hub, error) {
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
	h := &Hub{
		cfg:      cfg,
		options:  options,
		logger:   logger,
		now:      now,
		commands: make(chan Command, 256),
		done:     make(chan struct{}),
		state: hubState{
			round:        round,
			participants: make(map[string]*Participant),
			sessions:     make(map[string]*Session),
			connections:  make(map[string]*Connection),
			resumeTokens: make(map[string]string),
			shortIDs:     make(map[string]string),
		},
	}
	h.roundID.Store(round.ID)
	return h, nil
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

func (h *Hub) RoundID() string {
	value, _ := h.roundID.Load().(string)
	return value
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
	h.broadcastTopology()
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

func (h *Hub) handleMessage(epoch string, message protocol.Message) {
	conn, ok := h.state.connections[epoch]
	if !ok || !conn.IsOpen() {
		return
	}
	switch msg := message.(type) {
	case *protocol.Ping:
		h.touch(epoch, h.now())
		h.send(conn, &protocol.Pong{
			V:             1,
			Type:          "pong",
			ServerTimeUTC: h.now().UTC().Format(time.RFC3339Nano),
		})
	case *protocol.Join:
		h.handleJoin(conn, msg)
	case *protocol.Resume:
		h.sendError(conn, protocol.CodeResumeExpired, "resume is not available in this build", msg.RequestID, "")
	case *protocol.AdminAuth:
		h.handleAdminAuth(conn, msg)
	default:
		if conn.State == ConnectionTeacher {
			h.sendError(conn, protocol.CodeForbidden, "admin action is not available yet", requestIDOf(message), "")
			return
		}
		if err := h.requireStudent(conn); err != nil {
			h.sendError(conn, err.Code, err.Message, requestIDOf(message), sessionIDOf(message))
			return
		}
		if roundID := messageRoundID(message); roundID != "" && roundID != h.state.round.ID {
			h.sendError(conn, protocol.CodeStaleRound, "round has changed", requestIDOf(message), sessionIDOf(message))
			return
		}
		h.sendError(conn, protocol.CodeForbidden, "message is not available yet", requestIDOf(message), sessionIDOf(message))
	}
}

func (h *Hub) handleJoin(conn *Connection, msg *protocol.Join) {
	if conn.State != ConnectionPending {
		h.sendError(conn, protocol.CodeInvalidMessage, "connection already joined", msg.RequestID, "")
		return
	}
	if conn.Admin {
		h.sendError(conn, protocol.CodeForbidden, "admin connection cannot join as a student", msg.RequestID, "")
		return
	}
	if msg.RoundID != h.state.round.ID {
		h.sendError(conn, protocol.CodeStaleRound, "round has changed", msg.RequestID, "")
		return
	}
	if len(h.state.participants) >= h.cfg.MaxIdentities {
		h.sendError(conn, protocol.CodeCapacityReached, "identity capacity reached", msg.RequestID, "")
		return
	}
	online := 0
	for _, participant := range h.state.participants {
		if participant.IsOnline() {
			online++
		}
	}
	if online >= h.cfg.MaxParticipants {
		h.sendError(conn, protocol.CodeCapacityReached, "participant capacity reached", msg.RequestID, "")
		return
	}

	participantID, err := protocol.NewID()
	if err != nil {
		h.sendError(conn, protocol.CodeInternal, "cannot allocate identity", msg.RequestID, "")
		return
	}
	shortID, ok := h.allocateShortID(participantID)
	if !ok {
		h.sendError(conn, protocol.CodeInternal, "cannot allocate short id", msg.RequestID, "")
		return
	}
	resumeToken, err := protocol.NewToken()
	if err != nil {
		h.sendError(conn, protocol.CodeInternal, "cannot allocate resume token", msg.RequestID, "")
		return
	}
	nickname := strings.TrimSpace(msg.Nickname)
	color := ColorForIndex(h.state.nextColor)
	h.state.nextColor++
	participant := NewParticipant(participantID, nickname, shortID, color, h.now())
	participant.MarkOnline(conn.Epoch)
	h.state.participants[participantID] = &participant
	h.state.resumeTokens[resumeToken] = participantID
	h.state.topologyVersion++

	conn.ParticipantID = participantID
	conn.State = ConnectionStudent
	markPeerJoined(conn)

	view := h.publicSnapshot()
	h.send(conn, &protocol.Welcome{
		V:               1,
		Type:            "welcome",
		RequestID:       msg.RequestID,
		ParticipantID:   participantID,
		Nickname:        nickname,
		ShortID:         shortID,
		Color:           color,
		ResumeToken:     resumeToken,
		ConnectionEpoch: conn.Epoch,
		Round:           h.roundView(),
		TopologyVersion: h.state.topologyVersion,
		Participants:    view.participants,
		Sessions:        view.sessions,
	})
	h.broadcastTopology()
}

func (h *Hub) handleAdminAuth(conn *Connection, msg *protocol.AdminAuth) {
	if conn.State != ConnectionPending {
		h.sendError(conn, protocol.CodeInvalidMessage, "connection already authenticated", msg.RequestID, "")
		return
	}
	if !conn.Admin {
		h.sendError(conn, protocol.CodeForbidden, "admin role requires the teacher listener", msg.RequestID, "")
		return
	}
	if h.options.AdminToken == "" {
		h.sendError(conn, protocol.CodeUnauthorized, "admin token is not configured", msg.RequestID, "")
		if conn.Peer != nil {
			conn.Peer.Close("unauthorized")
		}
		return
	}
	if subtle.ConstantTimeCompare([]byte(msg.Token), []byte(h.options.AdminToken)) != 1 {
		h.sendError(conn, protocol.CodeUnauthorized, "invalid admin token", msg.RequestID, "")
		if conn.Peer != nil {
			conn.Peer.Close("unauthorized")
		}
		return
	}
	teachers := 0
	for _, connection := range h.state.connections {
		if connection.State == ConnectionTeacher {
			teachers++
		}
	}
	if teachers >= h.cfg.MaxTeachers {
		h.sendError(conn, protocol.CodeCapacityReached, "teacher capacity reached", msg.RequestID, "")
		return
	}

	conn.State = ConnectionTeacher
	markPeerJoined(conn)
	view := h.publicSnapshot()
	version := h.options.ServerVersion
	if version == "" {
		version = "dev"
	}
	h.send(conn, &protocol.AdminWelcome{
		V:               1,
		Type:            "admin.welcome",
		RequestID:       msg.RequestID,
		ConnectionEpoch: conn.Epoch,
		ServerVersion:   version,
		ProtocolVersion: 1,
		Round:           h.roundView(),
		TopologyVersion: h.state.topologyVersion,
		Participants:    view.participants,
		Sessions:        view.sessions,
	})
}

func (h *Hub) requireStudent(conn *Connection) *protocol.Error {
	if conn.State != ConnectionStudent || conn.ParticipantID == "" {
		return &protocol.Error{Code: protocol.CodeNotMember, Message: "connection has not joined"}
	}
	participant, ok := h.state.participants[conn.ParticipantID]
	if !ok || !participant.IsOnline() || participant.ConnectionEpoch != conn.Epoch {
		return &protocol.Error{Code: protocol.CodeStaleConnection, Message: "connection epoch is no longer current"}
	}
	return nil
}

type publicSnapshotData struct {
	participants []protocol.ParticipantView
	sessions     []protocol.SessionView
}

func (h *Hub) publicSnapshot() publicSnapshotData {
	participants := make([]protocol.ParticipantView, 0, len(h.state.participants))
	for _, participant := range h.state.participants {
		if participant.State != ParticipantOnline {
			continue
		}
		participants = append(participants, protocol.ParticipantView{
			ID:       participant.ID,
			Nickname: participant.Nickname,
			ShortID:  participant.ShortID,
			Color:    participant.Color,
			Online:   true,
		})
	}
	sort.Slice(participants, func(i, j int) bool { return participants[i].ID < participants[j].ID })

	sessions := make([]protocol.SessionView, 0, len(h.state.sessions))
	for _, session := range h.state.sessions {
		if session.State == SessionClosed {
			continue
		}
		sessions = append(sessions, protocol.SessionView{
			SessionID: session.ID,
			AID:       session.AID,
			BID:       session.BID,
			Mode:      session.Mode,
			State:     string(session.State),
		})
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].SessionID < sessions[j].SessionID })
	return publicSnapshotData{participants: participants, sessions: sessions}
}

func (h *Hub) broadcastTopology() {
	view := h.publicSnapshot()
	for _, conn := range h.state.connections {
		if conn.State != ConnectionStudent || conn.ParticipantID == "" {
			continue
		}
		participant, ok := h.state.participants[conn.ParticipantID]
		if !ok || !participant.IsOnline() {
			continue
		}
		h.send(conn, &protocol.TopologyChanged{
			V:               1,
			Type:            "topology.changed",
			RoundID:         h.state.round.ID,
			ConnectionEpoch: conn.Epoch,
			TopologyVersion: h.state.topologyVersion,
			Participants:    view.participants,
			Sessions:        view.sessions,
		})
	}
}

func (h *Hub) roundView() protocol.RoundView {
	return protocol.RoundView{ID: h.state.round.ID, Status: string(h.state.round.Status)}
}

func (h *Hub) allocateShortID(participantID string) (string, bool) {
	candidate := ShortIDFor(participantID)
	if _, exists := h.state.shortIDs[candidate]; !exists {
		h.state.shortIDs[candidate] = participantID
		return candidate, true
	}
	for attempt := 0; attempt < 100; attempt++ {
		randomID, err := protocol.NewID()
		if err != nil {
			return "", false
		}
		candidate = strings.ToUpper(randomID[:4])
		if _, exists := h.state.shortIDs[candidate]; !exists {
			h.state.shortIDs[candidate] = participantID
			return candidate, true
		}
	}
	return "", false
}

func (h *Hub) send(conn *Connection, message any) {
	if conn == nil || conn.Peer == nil {
		return
	}
	if err := conn.Peer.Send(message); err != nil {
		conn.Peer.Close("slow_client")
	}
}

func (h *Hub) sendError(conn *Connection, code protocol.Code, message, requestID, sessionID string) {
	h.send(conn, protocol.NewErrorMessage(&protocol.Error{
		Code:      code,
		Message:   message,
		RequestID: requestID,
		SessionID: sessionID,
	}))
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
			Admin:         conn.Admin,
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

type joinMarker interface {
	MarkJoined()
}

func markPeerJoined(conn *Connection) {
	if conn == nil || conn.Peer == nil {
		return
	}
	if marker, ok := conn.Peer.(joinMarker); ok {
		marker.MarkJoined()
	}
}

func requestIDOf(message protocol.Message) string {
	switch msg := message.(type) {
	case *protocol.Join:
		return msg.RequestID
	case *protocol.Resume:
		return msg.RequestID
	case *protocol.Leave:
		return msg.RequestID
	case *protocol.InviteCreate:
		return msg.RequestID
	case *protocol.InviteRespond:
		return msg.RequestID
	case *protocol.InviteCancel:
		return msg.RequestID
	case *protocol.SessionClose:
		return msg.RequestID
	case *protocol.KeyPublish:
		return msg.RequestID
	case *protocol.KeyConfirm:
		return msg.RequestID
	case *protocol.KeyReady:
		return msg.RequestID
	case *protocol.ChatSend:
		return msg.RequestID
	case *protocol.ChatReceipt:
		return msg.RequestID
	case *protocol.AdminAuth:
		return msg.RequestID
	case *protocol.ObserverSubscribe:
		return msg.RequestID
	case *protocol.RoundReset:
		return msg.RequestID
	case *protocol.ClassEnd:
		return msg.RequestID
	case *protocol.ClassStart:
		return msg.RequestID
	case *protocol.ServerShutdown:
		return msg.RequestID
	default:
		return ""
	}
}

func sessionIDOf(message protocol.Message) string {
	switch msg := message.(type) {
	case *protocol.InviteRespond:
		return msg.SessionID
	case *protocol.InviteCancel:
		return msg.SessionID
	case *protocol.SessionClose:
		return msg.SessionID
	case *protocol.KeyPublish:
		return msg.SessionID
	case *protocol.KeyConfirm:
		return msg.SessionID
	case *protocol.KeyReady:
		return msg.SessionID
	case *protocol.ChatSend:
		return msg.SessionID
	case *protocol.ChatReceipt:
		return msg.SessionID
	default:
		return ""
	}
}

func messageRoundID(message protocol.Message) string {
	switch msg := message.(type) {
	case *protocol.Join:
		return msg.RoundID
	case *protocol.Resume:
		return msg.RoundID
	case *protocol.Leave:
		return msg.RoundID
	case *protocol.InviteCreate:
		return msg.RoundID
	case *protocol.InviteRespond:
		return msg.RoundID
	case *protocol.InviteCancel:
		return msg.RoundID
	case *protocol.SessionClose:
		return msg.RoundID
	case *protocol.KeyPublish:
		return msg.RoundID
	case *protocol.KeyConfirm:
		return msg.RoundID
	case *protocol.KeyReady:
		return msg.RoundID
	case *protocol.ChatSend:
		return msg.RoundID
	case *protocol.ChatReceipt:
		return msg.RoundID
	default:
		return ""
	}
}
