package transport

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/config"
	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/hub"
	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/protocol"
	"github.com/coder/websocket"
)

var (
	ErrSlowClient       = errors.New("slow client")
	ErrConnectionClosed = errors.New("connection closed")
)

type Connection struct {
	cfg         config.Config
	ws          *websocket.Conn
	hub         *hub.Hub
	epoch       string
	admin       bool
	connectedAt time.Time

	queue       chan []byte
	queuedBytes atomic.Int64
	lastSeen    atomic.Int64
	closed      atomic.Bool
	closing     atomic.Bool
	joined      atomic.Bool

	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
	onClose   func(epoch string)
	limiter   *tokenBucket
}

func newConnection(parent context.Context, cfg config.Config, ws *websocket.Conn, h *hub.Hub, admin bool, onClose func(string)) (*Connection, error) {
	epoch, err := protocol.NewID()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	now := time.Now()
	count := cfg.SendQueueCount
	if count < 1 {
		count = 1
	}
	conn := &Connection{
		cfg:         cfg,
		ws:          ws,
		hub:         h,
		epoch:       epoch,
		admin:       admin,
		connectedAt: now,
		queue:       make(chan []byte, count),
		ctx:         ctx,
		cancel:      cancel,
		onClose:     onClose,
		limiter:     newTokenBucket(cfg.ControlRatePerSec, cfg.ControlBurst, now),
	}
	conn.lastSeen.Store(now.UnixNano())
	return conn, nil
}

func (c *Connection) HubConnection() *hub.Connection {
	return &hub.Connection{
		Epoch:       c.epoch,
		Admin:       c.admin,
		State:       hub.ConnectionPending,
		ConnectedAt: c.connectedAt,
		LastSeen:    c.connectedAt,
		Peer:        c,
	}
}

func (c *Connection) Epoch() string {
	return c.epoch
}

func (c *Connection) Send(message any) error {
	if c.closing.Load() || c.closed.Load() {
		return ErrConnectionClosed
	}
	data, err := encodeMessage(message)
	if err != nil {
		return err
	}
	maxMessage := c.cfg.MaxWSMessageBytes
	if maxMessage < 1 {
		maxMessage = 65536
	}
	if len(data) > maxMessage {
		return errors.New("outbound message too large")
	}
	maxBytes := c.cfg.SendQueueBytes
	if maxBytes < 1 {
		maxBytes = 256 << 10
	}
	added := c.queuedBytes.Add(int64(len(data)))
	if added > int64(maxBytes) {
		c.queuedBytes.Add(-int64(len(data)))
		return ErrSlowClient
	}
	select {
	case c.queue <- data:
		return nil
	default:
		c.queuedBytes.Add(-int64(len(data)))
		return ErrSlowClient
	}
}

func (c *Connection) Close(reason string) {
	c.closeOnce.Do(func() {
		c.closing.Store(true)
		go c.finishClose(reason)
	})
}

func (c *Connection) finishClose(reason string) {
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(c.queue) == 0 && c.queuedBytes.Load() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.closed.Store(true)
	c.cancel()
	_ = c.ws.CloseNow()
	if c.onClose != nil {
		c.onClose(c.epoch)
	}
}

func (c *Connection) MarkJoined() {
	c.joined.Store(true)
}

func (c *Connection) Serve() {
	go c.writeLoop()
	go c.heartbeatLoop()
	go c.joinTimeoutLoop()
	c.readLoop()
	c.Close("connection_closed")
}

func (c *Connection) readLoop() {
	limit := c.cfg.MaxWSMessageBytes
	if limit < 1 {
		limit = 65536
	}
	c.ws.SetReadLimit(int64(limit))

	for {
		typ, data, err := c.ws.Read(c.ctx)
		if err != nil {
			return
		}
		c.lastSeen.Store(time.Now().UnixNano())
		if typ != websocket.MessageText {
			c.Close("binary_message")
			return
		}
		if len(data) > limit {
			c.Close("message_too_large")
			return
		}
		message, parseErr := protocol.ParseClient(data, protocol.Limits{
			MaxBodyBytes:       c.cfg.MaxBodyBytes,
			MaxCiphertextBytes: c.cfg.MaxCiphertextBytes,
			MaxWSMessageBytes:  c.cfg.MaxWSMessageBytes,
			MaxNicknameRunes:   c.cfg.MaxNicknameRunes,
		})
		if parseErr != nil {
			c.sendError(parseErr)
			if parseErr.Code == protocol.CodeVersionMismatch || parseErr.Code == protocol.CodeMessageTooLarge {
				time.AfterFunc(150*time.Millisecond, func() { c.Close("invalid_message") })
				return
			}
			continue
		}
		if !c.limiter.allow(time.Now()) {
			c.sendError(&protocol.Error{Code: protocol.CodeRateLimited, Message: "too many messages"})
			continue
		}
		submitCtx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
		err = c.hub.Submit(submitCtx, hub.MessageCommand{Epoch: c.epoch, Message: message})
		cancel()
		if err != nil {
			c.Close("hub_unavailable")
			return
		}
	}
}

func (c *Connection) writeLoop() {
	timeout := c.cfg.WriteTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	for {
		select {
		case <-c.ctx.Done():
			return
		case data := <-c.queue:
			if c.closed.Load() {
				c.queuedBytes.Add(-int64(len(data)))
				return
			}
			writeCtx, cancel := context.WithTimeout(context.Background(), timeout)
			err := c.ws.Write(writeCtx, websocket.MessageText, data)
			cancel()
			c.queuedBytes.Add(-int64(len(data)))
			if err != nil {
				c.Close("write_failed")
				return
			}
		}
	}
}

func (c *Connection) heartbeatLoop() {
	interval := c.cfg.HeartbeatInterval
	dead := c.cfg.DeadTimeout
	if interval <= 0 {
		interval = 15 * time.Second
	}
	if dead <= 0 {
		dead = 45 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			last := time.Unix(0, c.lastSeen.Load())
			if time.Since(last) > dead {
				c.Close("heartbeat_timeout")
				return
			}
		}
	}
}

func (c *Connection) joinTimeoutLoop() {
	timeout := c.cfg.JoinTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-c.ctx.Done():
		return
	case <-timer.C:
		if c.joined.Load() {
			return
		}
		c.sendError(&protocol.Error{Code: protocol.CodeInvalidMessage, Message: "join timeout"})
		time.AfterFunc(150*time.Millisecond, func() { c.Close("join_timeout") })
	}
}

func (c *Connection) sendError(err *protocol.Error) {
	if err == nil {
		err = &protocol.Error{Code: protocol.CodeInternal, Message: "internal error"}
	}
	_ = c.Send(protocol.NewErrorMessage(err))
}

func encodeMessage(message any) ([]byte, error) {
	switch value := message.(type) {
	case []byte:
		return value, nil
	case json.RawMessage:
		return value, nil
	default:
		return json.Marshal(message)
	}
}

var _ hub.Peer = (*Connection)(nil)
