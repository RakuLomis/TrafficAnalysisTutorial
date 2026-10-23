package hub

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/config"
	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/observe"
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

	startedAt time.Time
	observe   *observe.Store
	state     hubState
}

type hubState struct {
	round           Round
	participants    map[string]*Participant
	sessions        map[string]*Session
	connections     map[string]*Connection
	resumeTokens    map[string]string
	shortIDs        map[string]string
	pairSessions    map[string]string
	sendSeq         map[string]*seqState
	dedup           map[string]*dedupCache
	pending         map[string]*pendingMessage
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

type TickCommand struct {
	Now time.Time
}

func (c TickCommand) apply(h *Hub) {
	h.expire(c.Now)
}

type seqState struct {
	last uint64
	has  bool
}

type dedupCache struct {
	entries map[string]string
	order   []string
	limit   int
}

func newDedupCache(limit int) *dedupCache {
	return &dedupCache{entries: make(map[string]string), limit: limit}
}

func (c *dedupCache) get(key string) (string, bool) {
	value, ok := c.entries[key]
	return value, ok
}

func (c *dedupCache) add(key, fingerprint string) {
	if _, exists := c.entries[key]; exists {
		c.entries[key] = fingerprint
		return
	}
	c.entries[key] = fingerprint
	c.order = append(c.order, key)
	for len(c.order) > c.limit {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
}

type pendingMessage struct {
	SessionID     string
	SenderID      string
	RecipientID   string
	MessageID     string
	Direction     string
	Mode          protocol.Mode
	Seq           string
	Text          *string
	NonceB64      string
	CiphertextB64 string
	AcceptedAt    time.Time
	ReceiptStatus string
}

type MessageCommand struct {
	Epoch   string
	Message protocol.Message
	WSBytes int
}

func (c MessageCommand) apply(h *Hub) {
	h.handleMessage(c.Epoch, c.Message, c.WSBytes)
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
		cfg:       cfg,
		options:   options,
		logger:    logger,
		now:       now,
		commands:  make(chan Command, 256),
		done:      make(chan struct{}),
		startedAt: round.CreatedAt,
		observe:   observe.New(cfg.SessionHistoryCount, cfg.SessionHistoryBytes, cfg.GlobalHistoryBytes),
		state: hubState{
			round:        round,
			participants: make(map[string]*Participant),
			sessions:     make(map[string]*Session),
			connections:  make(map[string]*Connection),
			resumeTokens: make(map[string]string),
			shortIDs:     make(map[string]string),
			pairSessions: make(map[string]string),
			sendSeq:      make(map[string]*seqState),
			dedup:        make(map[string]*dedupCache),
			pending:      make(map[string]*pendingMessage),
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

func (h *Hub) StartTimers(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				_ = h.Submit(ctx, TickCommand{Now: now})
			}
		}
	}()
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

func (h *Hub) handleMessage(epoch string, message protocol.Message, wsBytes int) {
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
			switch msg := message.(type) {
			case *protocol.ObserverSubscribe:
				h.handleObserverSubscribe(conn, msg)
			default:
				h.sendError(conn, protocol.CodeForbidden, "admin action is not available yet", requestIDOf(message), "")
			}
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
		switch msg := message.(type) {
		case *protocol.InviteCreate:
			h.handleInviteCreate(conn, msg)
		case *protocol.InviteRespond:
			h.handleInviteRespond(conn, msg)
		case *protocol.InviteCancel:
			h.handleInviteCancel(conn, msg)
		case *protocol.SessionClose:
			h.handleSessionClose(conn, msg)
		case *protocol.ChatSend:
			h.handleChatSend(conn, msg, wsBytes)
		case *protocol.ChatReceipt:
			h.handleChatReceipt(conn, msg)
		case *protocol.KeyPublish:
			h.handleKeyPublish(conn, msg)
		case *protocol.KeyConfirm:
			h.handleKeyConfirm(conn, msg)
		case *protocol.KeyReady:
			h.handleKeyReady(conn, msg)
		default:
			h.sendError(conn, protocol.CodeForbidden, "message is not available yet", requestIDOf(message), sessionIDOf(message))
		}
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

func (h *Hub) handleInviteCreate(conn *Connection, msg *protocol.InviteCreate) {
	sender, ok := h.state.participants[conn.ParticipantID]
	if !ok {
		h.sendError(conn, protocol.CodeNotMember, "sender is not a participant", msg.RequestID, "")
		return
	}
	target, ok := h.state.participants[msg.TargetID]
	if !ok || !target.IsOnline() {
		h.sendError(conn, protocol.CodeNotMember, "target is not online", msg.RequestID, "")
		return
	}
	if target.ID == sender.ID {
		h.sendError(conn, protocol.CodeInvalidMessage, "cannot invite yourself", msg.RequestID, "")
		return
	}
	pair := PairKey(sender.ID, target.ID)
	if sessionID, exists := h.state.pairSessions[pair]; exists {
		if session, ok := h.state.sessions[sessionID]; ok && session.IsOpen() {
			h.sendError(conn, protocol.CodeSessionExists, "session already exists", msg.RequestID, session.ID)
			h.sendSessionChanged(session)
			return
		}
		delete(h.state.pairSessions, pair)
	}
	if h.openSessionCount(sender.ID) >= h.cfg.MaxSessionsPerParticipant || h.openSessionCount(target.ID) >= h.cfg.MaxSessionsPerParticipant {
		h.sendError(conn, protocol.CodeCapacityReached, "session capacity reached", msg.RequestID, "")
		return
	}
	sessionID, err := protocol.NewID()
	if err != nil {
		h.sendError(conn, protocol.CodeInternal, "cannot allocate session", msg.RequestID, "")
		return
	}
	now := h.now()
	session, err := NewSession(sessionID, sender.ID, target.ID, msg.Mode, now, now.Add(h.cfg.InviteTimeout))
	if err != nil {
		h.sendError(conn, protocol.CodeInvalidMessage, "cannot create session", msg.RequestID, "")
		return
	}
	h.state.sessions[session.ID] = session
	h.state.pairSessions[pair] = session.ID
	h.state.topologyVersion++
	h.sendSessionChanged(session)
	h.broadcastTopology()
}

func (h *Hub) handleInviteRespond(conn *Connection, msg *protocol.InviteRespond) {
	session, ok := h.state.sessions[msg.SessionID]
	if !ok {
		h.sendError(conn, protocol.CodeNotMember, "session not found", msg.RequestID, msg.SessionID)
		return
	}
	if conn.ParticipantID != session.BID {
		h.sendError(conn, protocol.CodeNotMember, "only the invitee can respond", msg.RequestID, msg.SessionID)
		return
	}
	if session.State != SessionInvited {
		code := protocol.CodeInvalidMessage
		message := "session is not waiting for a response"
		if session.State == SessionClosed && session.ClosedReason == "invite_timeout" {
			code = protocol.CodeInviteExpired
			message = "invite has expired"
		}
		h.sendError(conn, code, message, msg.RequestID, msg.SessionID)
		return
	}
	now := h.now()
	if !session.ExpiresAt.IsZero() && !now.Before(session.ExpiresAt) {
		h.closeSession(session, "invite_timeout")
		h.sendError(conn, protocol.CodeInviteExpired, "invite has expired", msg.RequestID, msg.SessionID)
		return
	}
	if msg.Accept == nil {
		h.sendError(conn, protocol.CodeInvalidMessage, "accept is required", msg.RequestID, msg.SessionID)
		return
	}
	if *msg.Accept {
		session.Accept(now.Add(h.cfg.KeyTimeout))
		h.state.topologyVersion++
		h.sendSessionChanged(session)
		h.broadcastTopology()
		return
	}
	h.closeSession(session, "rejected")
}

func (h *Hub) handleInviteCancel(conn *Connection, msg *protocol.InviteCancel) {
	session, ok := h.state.sessions[msg.SessionID]
	if !ok || !session.Contains(conn.ParticipantID) {
		h.sendError(conn, protocol.CodeNotMember, "session not found", msg.RequestID, msg.SessionID)
		return
	}
	if conn.ParticipantID != session.AID {
		h.sendError(conn, protocol.CodeNotMember, "only the inviter can cancel", msg.RequestID, msg.SessionID)
		return
	}
	if session.State != SessionInvited {
		h.sendError(conn, protocol.CodeInvalidMessage, "session is not an invite", msg.RequestID, msg.SessionID)
		return
	}
	h.closeSession(session, "cancelled")
}

func (h *Hub) handleSessionClose(conn *Connection, msg *protocol.SessionClose) {
	session, ok := h.state.sessions[msg.SessionID]
	if !ok || !session.Contains(conn.ParticipantID) {
		h.sendError(conn, protocol.CodeNotMember, "session not found", msg.RequestID, msg.SessionID)
		return
	}
	if session.State == SessionClosed {
		h.sendSessionChanged(session)
		return
	}
	h.closeSession(session, msg.Reason)
}

func (h *Hub) handleObserverSubscribe(conn *Connection, msg *protocol.ObserverSubscribe) {
	if msg.SessionID == nil {
		conn.Subscription = ""
		h.send(conn, &protocol.ObserverSnapshot{
			V:         1,
			Type:      "observer.snapshot",
			RequestID: msg.RequestID,
			SessionID: "",
			Messages:  []protocol.ObserverRecord{},
		})
		return
	}
	session, ok := h.state.sessions[*msg.SessionID]
	if !ok {
		h.sendError(conn, protocol.CodeNotMember, "session not found", msg.RequestID, *msg.SessionID)
		return
	}
	conn.Subscription = session.ID
	snapshot := protocol.ObserverSnapshot{
		V:         1,
		Type:      "observer.snapshot",
		RequestID: msg.RequestID,
		SessionID: session.ID,
		Mode:      string(session.Mode),
		Messages:  []protocol.ObserverRecord{},
	}
	if stored, ok := h.observe.Snapshot(session.ID); ok {
		snapshot = stored
		snapshot.V = 1
		snapshot.Type = "observer.snapshot"
		snapshot.RequestID = msg.RequestID
		snapshot.SessionID = session.ID
		snapshot.Mode = string(session.Mode)
		if snapshot.Messages == nil {
			snapshot.Messages = []protocol.ObserverRecord{}
		}
	}
	h.send(conn, &snapshot)
}
func (h *Hub) handleKeyPublish(conn *Connection, msg *protocol.KeyPublish) {
	session, ok := h.state.sessions[msg.SessionID]
	if !ok || !session.Contains(conn.ParticipantID) {
		h.sendError(conn, protocol.CodeNotMember, "session not found", msg.RequestID, msg.SessionID)
		return
	}
	if session.Mode != protocol.ModeEncrypted || session.State != SessionNegotiating {
		h.sendError(conn, protocol.CodeNotActive, "session is not negotiating", msg.RequestID, msg.SessionID)
		return
	}
	key, err := base64.StdEncoding.DecodeString(msg.PublicKeyB64)
	if err != nil || len(key) != 32 {
		h.sendError(conn, protocol.CodeInvalidMessage, "public key must be 32 bytes", msg.RequestID, msg.SessionID)
		return
	}
	senderIsA := conn.ParticipantID == session.AID
	existing := session.BPublicKey
	if senderIsA {
		existing = session.APublicKey
	}
	if existing != nil {
		if !bytes.Equal(existing, key) {
			h.closeSession(session, "crypto_failed")
			h.sendError(conn, protocol.CodeCryptoFailed, "public key changed", msg.RequestID, msg.SessionID)
			return
		}
	} else if senderIsA {
		session.APublicKey = append([]byte(nil), key...)
	} else {
		session.BPublicKey = append([]byte(nil), key...)
	}

	peerConn, ok := h.connectionFor(session.Counterpart(conn.ParticipantID))
	if !ok {
		return
	}
	h.send(peerConn, &protocol.KeyPublishDeliver{
		V:               1,
		Type:            "key.publish",
		RoundID:         h.state.round.ID,
		ConnectionEpoch: peerConn.Epoch,
		SessionID:       session.ID,
		SenderID:        conn.ParticipantID,
		PublicKeyB64:    msg.PublicKeyB64,
	})
}

func (h *Hub) handleKeyConfirm(conn *Connection, msg *protocol.KeyConfirm) {
	session, ok := h.state.sessions[msg.SessionID]
	if !ok || !session.Contains(conn.ParticipantID) {
		h.sendError(conn, protocol.CodeNotMember, "session not found", msg.RequestID, msg.SessionID)
		return
	}
	if session.Mode != protocol.ModeEncrypted || session.State != SessionNegotiating {
		h.sendError(conn, protocol.CodeNotActive, "session is not negotiating", msg.RequestID, msg.SessionID)
		return
	}
	if session.APublicKey == nil || session.BPublicKey == nil {
		h.sendError(conn, protocol.CodeNotActive, "public keys are not ready", msg.RequestID, msg.SessionID)
		return
	}
	counter, err := validateEncryptedNonce(msg.NonceB64, msg.Seq, conn.ParticipantID == session.AID)
	if err != nil || counter != 0 {
		h.closeSession(session, "crypto_failed")
		h.sendError(conn, protocol.CodeCryptoFailed, "invalid key-confirm nonce or seq", msg.RequestID, msg.SessionID)
		return
	}
	peerConn, ok := h.connectionFor(session.Counterpart(conn.ParticipantID))
	if !ok {
		return
	}
	h.send(peerConn, &protocol.KeyConfirmDeliver{
		V:               1,
		Type:            "key.confirm",
		RoundID:         h.state.round.ID,
		ConnectionEpoch: peerConn.Epoch,
		SessionID:       session.ID,
		SenderID:        conn.ParticipantID,
		Seq:             msg.Seq,
		NonceB64:        msg.NonceB64,
		CiphertextB64:   msg.CiphertextB64,
	})
}

func (h *Hub) handleKeyReady(conn *Connection, msg *protocol.KeyReady) {
	session, ok := h.state.sessions[msg.SessionID]
	if !ok || !session.Contains(conn.ParticipantID) {
		h.sendError(conn, protocol.CodeNotMember, "session not found", msg.RequestID, msg.SessionID)
		return
	}
	if session.Mode != protocol.ModeEncrypted || session.State != SessionNegotiating {
		h.sendError(conn, protocol.CodeNotActive, "session is not negotiating", msg.RequestID, msg.SessionID)
		return
	}
	if msg.Status == "failed" {
		h.closeSession(session, "crypto_failed")
		h.sendError(conn, protocol.CodeCryptoFailed, "client reported key verification failure", msg.RequestID, msg.SessionID)
		return
	}
	if session.APublicKey == nil || session.BPublicKey == nil {
		h.sendError(conn, protocol.CodeNotActive, "public keys are not ready", msg.RequestID, msg.SessionID)
		return
	}
	if conn.ParticipantID == session.AID {
		session.AReady = true
	} else {
		session.BReady = true
	}
	if !session.AReady || !session.BReady {
		return
	}
	session.Activate()
	h.state.topologyVersion++
	h.sendSessionChanged(session)
	h.broadcastTopology()
}

func validateEncryptedNonce(nonceB64, seq string, senderIsA bool) (uint64, error) {
	nonce, err := base64.StdEncoding.DecodeString(nonceB64)
	if err != nil || len(nonce) != 24 {
		return 0, errors.New("invalid nonce")
	}
	expectedDirection := byte(1)
	if senderIsA {
		expectedDirection = 0
	}
	if nonce[0] != expectedDirection {
		return 0, errors.New("invalid nonce direction")
	}
	counter := binary.BigEndian.Uint64(nonce[16:24])
	if strconv.FormatUint(counter, 10) != seq {
		return 0, errors.New("seq does not match nonce counter")
	}
	return counter, nil
}
func (h *Hub) handleChatSend(conn *Connection, msg *protocol.ChatSend, wsBytes int) {
	session, ok := h.state.sessions[msg.SessionID]
	if !ok || !session.Contains(conn.ParticipantID) {
		h.sendError(conn, protocol.CodeNotMember, "session not found", msg.RequestID, msg.SessionID)
		return
	}
	if session.State != SessionActive {
		h.sendError(conn, protocol.CodeNotActive, "session is not active", msg.RequestID, msg.SessionID)
		return
	}
	senderID := conn.ParticipantID
	recipientID := session.Counterpart(senderID)
	direction := session.Direction(senderID)

	var fingerprint string
	switch session.Mode {
	case protocol.ModePlaintext:
		if msg.Text == nil {
			h.sendError(conn, protocol.CodeModeMismatch, "plaintext session requires text", msg.RequestID, msg.SessionID)
			return
		}
		fingerprint = "p|" + msg.Seq + "|" + *msg.Text
	case protocol.ModeEncrypted:
		if msg.Text != nil {
			h.sendError(conn, protocol.CodeModeMismatch, "encrypted session must not carry text", msg.RequestID, msg.SessionID)
			return
		}
		counter, nonceErr := validateEncryptedNonce(msg.NonceB64, msg.Seq, senderID == session.AID)
		if nonceErr != nil || counter == 0 {
			h.closeSession(session, "crypto_failed")
			h.sendError(conn, protocol.CodeCryptoFailed, "invalid nonce or seq", msg.RequestID, msg.SessionID)
			return
		}
		fingerprint = "e|" + msg.Seq + "|" + msg.NonceB64 + "|" + msg.CiphertextB64
	default:
		h.sendError(conn, protocol.CodeModeMismatch, "unsupported session mode", msg.RequestID, msg.SessionID)
		return
	}

	directionKey := session.ID + "|" + senderID
	cache := h.state.dedup[directionKey]
	if cache == nil {
		cache = newDedupCache(128)
		h.state.dedup[directionKey] = cache
	}
	if previous, found := cache.get(msg.MessageID); found {
		if previous != fingerprint {
			h.sendError(conn, protocol.CodeIDConflict, "message id reused with different payload", msg.RequestID, msg.SessionID)
			return
		}
		h.resendAccepted(conn, session, senderID, recipientID, msg)
		return
	}

	seq, err := protocol.ParseSeq(msg.Seq)
	if err != nil {
		h.sendError(conn, protocol.CodeInvalidMessage, "invalid seq", msg.RequestID, msg.SessionID)
		return
	}
	seqKey := session.ID + "|" + senderID
	state := h.state.sendSeq[seqKey]
	if state == nil {
		state = &seqState{}
		h.state.sendSeq[seqKey] = state
	}
	if state.has && seq <= state.last {
		h.sendError(conn, protocol.CodeInvalidMessage, "seq must increase", msg.RequestID, msg.SessionID)
		return
	}
	state.last = seq
	state.has = true
	cache.add(msg.MessageID, fingerprint)

	acceptedAt := h.now()
	pending := &pendingMessage{
		SessionID:     session.ID,
		SenderID:      senderID,
		RecipientID:   recipientID,
		MessageID:     msg.MessageID,
		Direction:     direction,
		Mode:          session.Mode,
		Seq:           msg.Seq,
		Text:          msg.Text,
		NonceB64:      msg.NonceB64,
		CiphertextB64: msg.CiphertextB64,
		AcceptedAt:    acceptedAt,
		ReceiptStatus: "pending",
	}
	h.state.pending[pendingKey(session.ID, senderID, msg.MessageID)] = pending

	h.send(conn, &protocol.ChatAccepted{
		V:               1,
		Type:            "chat.accepted",
		RequestID:       msg.RequestID,
		RoundID:         h.state.round.ID,
		ConnectionEpoch: conn.Epoch,
		SessionID:       session.ID,
		MessageID:       msg.MessageID,
		Seq:             msg.Seq,
		AcceptedAtUTC:   formatUTC(acceptedAt),
	})
	h.recordObservation(session, senderID, direction, msg, acceptedAt, wsBytes)
	h.broadcastActivity(protocol.ActivityEvent{
		SessionID: session.ID,
		MessageID: msg.MessageID,
		SenderID:  senderID,
		Direction: direction,
		AtUTC:     formatUTC(acceptedAt),
	})

	recipientConn, ok := h.connectionFor(recipientID)
	if !ok {
		pending.ReceiptStatus = "failed"
		h.sendStatus(pending, "failed", h.now())
		return
	}
	deliver := &protocol.ChatDeliver{
		V:               1,
		Type:            "chat.deliver",
		RoundID:         h.state.round.ID,
		ConnectionEpoch: recipientConn.Epoch,
		SessionID:       session.ID,
		MessageID:       msg.MessageID,
		SenderID:        senderID,
		Seq:             msg.Seq,
		Text:            msg.Text,
		NonceB64:        msg.NonceB64,
		CiphertextB64:   msg.CiphertextB64,
		SentAtUTC:       formatUTC(acceptedAt),
	}
	if err := h.send(recipientConn, deliver); err != nil {
		pending.ReceiptStatus = "failed"
		h.sendStatus(pending, "failed", h.now())
	}
}

func (h *Hub) resendAccepted(conn *Connection, session *Session, senderID, recipientID string, msg *protocol.ChatSend) {
	acceptedAt := h.now()
	if pending, ok := h.state.pending[pendingKey(session.ID, senderID, msg.MessageID)]; ok {
		acceptedAt = pending.AcceptedAt
	}
	h.send(conn, &protocol.ChatAccepted{
		V:               1,
		Type:            "chat.accepted",
		RequestID:       msg.RequestID,
		RoundID:         h.state.round.ID,
		ConnectionEpoch: conn.Epoch,
		SessionID:       session.ID,
		MessageID:       msg.MessageID,
		Seq:             msg.Seq,
		AcceptedAtUTC:   formatUTC(acceptedAt),
	})
}

func (h *Hub) recordObservation(session *Session, senderID, direction string, msg *protocol.ChatSend, acceptedAt time.Time, wsBytes int) {
	payloadBytes := 0
	var text *string
	var nonce *string
	var ciphertext *string
	if session.Mode == protocol.ModePlaintext && msg.Text != nil {
		payloadBytes = len([]byte(*msg.Text))
		text = msg.Text
	} else if session.Mode == protocol.ModeEncrypted {
		if decoded, err := base64.StdEncoding.DecodeString(msg.CiphertextB64); err == nil {
			payloadBytes = len(decoded)
		}
		nonceValue := msg.NonceB64
		cipherValue := msg.CiphertextB64
		nonce = &nonceValue
		ciphertext = &cipherValue
	}
	mono := acceptedAt.Sub(h.startedAt).Milliseconds()
	if mono < 0 {
		mono = 0
	}
	record := protocol.ObserverRecord{
		SessionID:      session.ID,
		MessageID:      msg.MessageID,
		SenderID:       senderID,
		Direction:      direction,
		Mode:           string(session.Mode),
		PayloadBytes:   payloadBytes,
		WSMessageBytes: wsBytes,
		AtUTC:          formatUTC(acceptedAt),
		MonoMS:         mono,
		ReceiptStatus:  "pending",
		Text:           text,
		NonceB64:       nonce,
		CiphertextB64:  ciphertext,
	}
	stored := h.observe.Add(record)
	h.notifyObserver(stored, false)
}

func (h *Hub) updateObservationStatus(sessionID, messageID, status string) {
	stored, changed := h.observe.UpdateReceipt(sessionID, messageID, status)
	if changed {
		h.notifyObserver(stored, true)
	}
}

func (h *Hub) notifyObserver(record protocol.ObserverRecord, update bool) {
	stats := protocol.ObserverStats{}
	truncated := false
	if snapshot, ok := h.observe.Snapshot(record.SessionID); ok {
		stats = snapshot.Stats
		truncated = snapshot.Truncated
	}
	for _, conn := range h.state.connections {
		if conn.State != ConnectionTeacher || conn.Subscription != record.SessionID {
			continue
		}
		h.send(conn, &protocol.ObserverEvent{
			V:         1,
			Type:      "observer.event",
			SessionID: record.SessionID,
			Message:   record,
			Update:    update,
			Stats:     stats,
			Truncated: truncated,
		})
	}
}
func (h *Hub) handleChatReceipt(conn *Connection, msg *protocol.ChatReceipt) {
	session, ok := h.state.sessions[msg.SessionID]
	if !ok || !session.Contains(conn.ParticipantID) {
		h.sendError(conn, protocol.CodeNotMember, "session not found", msg.RequestID, msg.SessionID)
		return
	}
	recipientID := conn.ParticipantID
	senderID := session.Counterpart(recipientID)
	pending, ok := h.state.pending[pendingKey(session.ID, senderID, msg.MessageID)]
	if !ok || pending.RecipientID != recipientID {
		h.sendError(conn, protocol.CodeNotMember, "message not found for this recipient", msg.RequestID, msg.SessionID)
		return
	}
	if msg.Status == "processed" {
		if pending.ReceiptStatus == "confirmed" {
			return
		}
		pending.ReceiptStatus = "confirmed"
		h.sendStatus(pending, "confirmed", h.now())
		h.updateObservationStatus(session.ID, msg.MessageID, "confirmed")
		return
	}
	if pending.ReceiptStatus != "failed" {
		pending.ReceiptStatus = "failed"
		h.sendStatus(pending, "failed", h.now())
	}
	h.updateObservationStatus(session.ID, msg.MessageID, "failed")
	h.closeSession(session, "crypto_failed")
}

func (h *Hub) closeSession(session *Session, reason string) {
	if session == nil || session.State == SessionClosed {
		return
	}
	session.Close(reason)
	delete(h.state.pairSessions, PairKey(session.AID, session.BID))
	h.state.topologyVersion++
	h.sendSessionChanged(session)
	h.broadcastTopology()
	h.failPendingForSession(session, h.now())
}

func (h *Hub) failPendingForSession(session *Session, now time.Time) {
	for _, pending := range h.state.pending {
		if pending.SessionID != session.ID {
			continue
		}
		if pending.ReceiptStatus == "pending" || pending.ReceiptStatus == "unconfirmed" {
			pending.ReceiptStatus = "failed"
			h.sendStatus(pending, "failed", now)
			h.updateObservationStatus(pending.SessionID, pending.MessageID, "failed")
		}
	}
}

func (h *Hub) expire(now time.Time) {
	for _, session := range h.state.sessions {
		if session.State != SessionInvited && session.State != SessionNegotiating {
			continue
		}
		if session.ExpiresAt.IsZero() || now.Before(session.ExpiresAt) {
			continue
		}
		reason := "invite_timeout"
		if session.State == SessionNegotiating {
			reason = "key_timeout"
		}
		h.closeSession(session, reason)
	}
	ackTimeout := h.cfg.AckTimeout
	if ackTimeout <= 0 {
		ackTimeout = 5 * time.Second
	}
	for _, pending := range h.state.pending {
		if pending.ReceiptStatus != "pending" {
			continue
		}
		if !now.Before(pending.AcceptedAt.Add(ackTimeout)) {
			pending.ReceiptStatus = "unconfirmed"
			h.sendStatus(pending, "unconfirmed", now)
			h.updateObservationStatus(pending.SessionID, pending.MessageID, "unconfirmed")
		}
	}
}

func (h *Hub) sendSessionChanged(session *Session) {
	view := h.sessionView(session)
	for _, participantID := range []string{session.AID, session.BID} {
		conn, ok := h.connectionFor(participantID)
		if !ok {
			continue
		}
		h.send(conn, &protocol.SessionChanged{
			V:               1,
			Type:            "session.changed",
			RoundID:         h.state.round.ID,
			ConnectionEpoch: conn.Epoch,
			Session:         view,
		})
	}
}

func (h *Hub) sessionView(session *Session) protocol.SessionView {
	return protocol.SessionView{
		SessionID:    session.ID,
		AID:          session.AID,
		BID:          session.BID,
		Mode:         session.Mode,
		State:        string(session.State),
		CreatedAtUTC: formatUTC(session.CreatedAt),
		ExpiresAtUTC: formatUTC(session.ExpiresAt),
		ClosedReason: session.ClosedReason,
	}
}

func (h *Hub) sendStatus(pending *pendingMessage, status string, at time.Time) {
	conn, ok := h.connectionFor(pending.SenderID)
	if !ok {
		return
	}
	h.send(conn, &protocol.ChatStatus{
		V:               1,
		Type:            "chat.status",
		RoundID:         h.state.round.ID,
		ConnectionEpoch: conn.Epoch,
		SessionID:       pending.SessionID,
		MessageID:       pending.MessageID,
		Status:          status,
		AtUTC:           formatUTC(at),
	})
}

func (h *Hub) broadcastActivity(event protocol.ActivityEvent) {
	for _, conn := range h.state.connections {
		if conn.State != ConnectionStudent || conn.ParticipantID == "" {
			continue
		}
		participant, ok := h.state.participants[conn.ParticipantID]
		if !ok || !participant.IsOnline() {
			continue
		}
		h.send(conn, &protocol.ActivityBatch{
			V:               1,
			Type:            "activity.batch",
			RoundID:         h.state.round.ID,
			ConnectionEpoch: conn.Epoch,
			Events:          []protocol.ActivityEvent{event},
		})
	}
}

func (h *Hub) connectionFor(participantID string) (*Connection, bool) {
	participant, ok := h.state.participants[participantID]
	if !ok || !participant.IsOnline() || participant.ConnectionEpoch == "" {
		return nil, false
	}
	conn, ok := h.state.connections[participant.ConnectionEpoch]
	if !ok || !conn.IsOpen() || conn.ParticipantID != participantID {
		return nil, false
	}
	return conn, true
}

func (h *Hub) openSessionCount(participantID string) int {
	count := 0
	for _, session := range h.state.sessions {
		if session.IsOpen() && session.Contains(participantID) {
			count++
		}
	}
	return count
}

func pendingKey(sessionID, senderID, messageID string) string {
	return sessionID + "|" + senderID + "|" + messageID
}

func formatUTC(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format("2006-01-02T15:04:05.000Z")
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
		a, okA := h.state.participants[session.AID]
		b, okB := h.state.participants[session.BID]
		if !okA || !okB || !a.IsOnline() || !b.IsOnline() {
			continue
		}
		sessions = append(sessions, h.sessionView(session))
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].SessionID < sessions[j].SessionID })
	return publicSnapshotData{participants: participants, sessions: sessions}
}

func (h *Hub) broadcastTopology() {
	view := h.publicSnapshot()
	for _, conn := range h.state.connections {
		if conn.State == ConnectionTeacher {
			h.send(conn, &protocol.TopologyChanged{
				V:               1,
				Type:            "topology.changed",
				RoundID:         h.state.round.ID,
				ConnectionEpoch: conn.Epoch,
				TopologyVersion: h.state.topologyVersion,
				Participants:    view.participants,
				Sessions:        view.sessions,
			})
			continue
		}
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

func (h *Hub) send(conn *Connection, message any) error {
	if conn == nil || conn.Peer == nil {
		return errors.New("connection has no peer")
	}
	if err := conn.Peer.Send(message); err != nil {
		conn.Peer.Close("slow_client")
		return err
	}
	return nil
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
	for _, conn := range h.state.connections {
		conn.Close()
		if conn.Peer != nil {
			conn.Peer.Close("server_shutdown")
		}
	}
	h.state.connections = make(map[string]*Connection)
	h.logger.Debug("hub stopped", "round_id", h.state.round.ID)
	h.markDone()
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
