package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/protocol"
	"github.com/coder/websocket"
)

type client struct {
	index int
	ws    *websocket.Conn

	mu        sync.Mutex
	sendMu    sync.Mutex
	epoch     string
	id        string
	roundID   string
	sessionID string
	mode      string
	role      string

	seq     uint64
	counter uint64
	pending map[string]time.Time

	latencies []time.Duration
	failures  int
	errors    int

	welcomeCh chan protocol.Welcome
	sessionCh chan protocol.SessionView
	peerKeyCh chan struct{}
}

func main() {
	url := flag.String("url", "ws://127.0.0.1:8080/ws", "student WebSocket URL")
	origin := flag.String("origin", "http://127.0.0.1:8080", "Origin header")
	clientsFlag := flag.Int("clients", 100, "number of student connections")
	duration := flag.Duration("duration", 5*time.Minute, "load duration")
	interval := flag.Duration("interval", 5*time.Second, "send interval per client")
	encryptedPairs := flag.Int("encrypted-pairs", 25, "number of encrypted pairs (rest plaintext)")
	flag.Parse()

	if *clientsFlag < 2 || *clientsFlag%2 != 0 {
		fmt.Fprintln(os.Stderr, "clients must be an even number >= 2")
		os.Exit(1)
	}
	roundID, err := fetchRoundID(*url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetch round: %v\n", err)
		os.Exit(1)
	}

	clients := make([]*client, *clientsFlag)
	var wg sync.WaitGroup
	var setupErr error
	var errMu sync.Mutex
	for i := 0; i < *clientsFlag; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			c, err := newClient(index, *url, *origin, roundID)
			if err != nil {
				errMu.Lock()
				if setupErr == nil {
					setupErr = fmt.Errorf("client %d: %w", index, err)
				}
				errMu.Unlock()
				return
			}
			clients[index] = c
		}(i)
	}
	wg.Wait()
	if setupErr != nil {
		fmt.Fprintf(os.Stderr, "setup failed: %v\n", setupErr)
		os.Exit(1)
	}

	pairs := *clientsFlag / 2
	if *encryptedPairs > pairs {
		*encryptedPairs = pairs
	}
	for i := 0; i < pairs; i++ {
		a := clients[i*2]
		b := clients[i*2+1]
		mode := "plaintext"
		if i < *encryptedPairs {
			mode = "encrypted"
		}
		if err := setupSession(a, b, mode); err != nil {
			fmt.Fprintf(os.Stderr, "session %d (%s): %v\n", i, mode, err)
			os.Exit(1)
		}
	}
	fmt.Printf("ready: clients=%d pairs=%d encrypted=%d\n", len(clients), pairs, *encryptedPairs)

	stop := time.Now().Add(*duration)
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	sent := 0
	for now := range ticker.C {
		if now.After(stop) {
			break
		}
		for _, c := range clients {
			if err := c.sendChat(); err != nil {
				c.recordError(err)
			} else {
				sent++
			}
		}
	}

	time.Sleep(6 * time.Second)
	var latencies []time.Duration
	failures := 0
	errors := 0
	for _, c := range clients {
		c.mu.Lock()
		latencies = append(latencies, c.latencies...)
		failures += c.failures
		errors += c.errors
		c.mu.Unlock()
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	report := map[string]any{
		"clients":           len(clients),
		"pairs":             pairs,
		"encrypted_pairs":   *encryptedPairs,
		"duration_seconds":  duration.Seconds(),
		"interval_seconds":  interval.Seconds(),
		"messages_sent":     sent,
		"confirmed":         len(latencies),
		"failures":          failures,
		"errors":            errors,
		"p50_ms":            percentile(latencies, 0.50),
		"p95_ms":            percentile(latencies, 0.95),
		"p99_ms":            percentile(latencies, 0.99),
		"max_ms":            percentile(latencies, 1.0),
	}
	data, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(data))
	for _, c := range clients {
		if c != nil && c.ws != nil {
			_ = c.ws.Close(websocket.StatusNormalClosure, "done")
		}
	}
}

func newClient(index int, url, origin, roundID string) (*client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{origin}},
	})
	if err != nil {
		return nil, err
	}
	c := &client{
		index:     index,
		ws:        ws,
		roundID:   roundID,
		pending:   make(map[string]time.Time),
		welcomeCh: make(chan protocol.Welcome, 1),
		sessionCh: make(chan protocol.SessionView, 64),
		peerKeyCh: make(chan struct{}, 4),
	}
	go c.readLoop()
	if err := c.send(&protocol.Join{
		V:         1,
		Type:      "join",
		RequestID: randomID(),
		RoundID:   roundID,
		Nickname:  fmt.Sprintf("load-%03d", index),
	}); err != nil {
		return nil, err
	}
	select {
	case welcome := <-c.welcomeCh:
		c.mu.Lock()
		c.epoch = welcome.ConnectionEpoch
		c.id = welcome.ParticipantID
		c.mu.Unlock()
		return c, nil
	case <-time.After(15 * time.Second):
		return nil, fmt.Errorf("join timeout")
	}
}

func (c *client) readLoop() {
	ctx := context.Background()
	for {
		typ, data, err := c.ws.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &head); err != nil {
			continue
		}
		switch head.Type {
		case "welcome":
			var message protocol.Welcome
			if json.Unmarshal(data, &message) == nil {
				select {
				case c.welcomeCh <- message:
				default:
				}
			}
		case "session.changed":
			var message protocol.SessionChanged
			if json.Unmarshal(data, &message) == nil {
				select {
				case c.sessionCh <- message.Session:
				default:
				}
			}
		case "key.publish":
			select {
			case c.peerKeyCh <- struct{}{}:
			default:
			}
		case "chat.deliver":
			var message protocol.ChatDeliver
			if json.Unmarshal(data, &message) == nil {
				c.sendReceipt(message.SessionID, message.MessageID)
			}
		case "chat.status":
			var message protocol.ChatStatus
			if json.Unmarshal(data, &message) == nil {
				c.recordStatus(message)
			}
		case "error":
			var message protocol.ErrorMessage
			if json.Unmarshal(data, &message) == nil {
				c.recordError(fmt.Errorf("%s: %s", message.Code, message.Message))
			}
		}
	}
}

func (c *client) send(message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.ws.Write(ctx, websocket.MessageText, data)
}

func (c *client) sendReceipt(sessionID, messageID string) {
	c.mu.Lock()
	epoch := c.epoch
	c.mu.Unlock()
	_ = c.send(&protocol.ChatReceipt{
		SessionCommand: protocol.SessionCommand{
			Joined: protocol.Joined{
				V:               1,
				Type:            "chat.receipt",
				RequestID:       randomID(),
				RoundID:         c.roundID,
				ConnectionEpoch: epoch,
			},
			SessionID: sessionID,
		},
		MessageID: messageID,
		Status:    "processed",
	})
}

func (c *client) recordStatus(message protocol.ChatStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	sent, ok := c.pending[message.MessageID]
	if !ok {
		return
	}
	delete(c.pending, message.MessageID)
	switch message.Status {
	case "confirmed":
		c.latencies = append(c.latencies, time.Since(sent))
	case "failed", "unconfirmed":
		c.failures++
	}
}

func (c *client) recordError(err error) {
	c.mu.Lock()
	c.errors++
	if c.errors <= 3 {
		fmt.Fprintf(os.Stderr, "client %d error: %v\n", c.index, err)
	}
	c.mu.Unlock()
}

func (c *client) sendChat() error {
	c.mu.Lock()
	if c.sessionID == "" || c.mode == "" {
		c.mu.Unlock()
		return nil
	}
	sessionID := c.sessionID
	mode := c.mode
	epoch := c.epoch
	seq := c.seq
	c.seq++
	messageID := randomID()
	c.pending[messageID] = time.Now()
	c.mu.Unlock()

	if mode == "plaintext" {
		text := fmt.Sprintf("load-%03d-%d", c.index, seq)
		return c.send(&protocol.ChatSend{
			SessionCommand: protocol.SessionCommand{
				Joined: protocol.Joined{V: 1, Type: "chat.send", RequestID: randomID(), RoundID: c.roundID, ConnectionEpoch: epoch},
				SessionID: sessionID,
			},
			MessageID: messageID,
			Seq:       strconv.FormatUint(seq, 10),
			Text:      &text,
		})
	}

	c.mu.Lock()
	counter := c.counter
	c.counter++
	c.mu.Unlock()
	nonce := make([]byte, 24)
	if c.role == "a" {
		nonce[0] = 0
	} else {
		nonce[0] = 1
	}
	for i := 1; i < 16; i++ {
		nonce[i] = byte(c.index)
	}
	binary.BigEndian.PutUint64(nonce[16:24], counter)
	ciphertext := make([]byte, 32)
	ciphertext[0] = byte(c.index)
	return c.send(&protocol.ChatSend{
		SessionCommand: protocol.SessionCommand{
			Joined: protocol.Joined{V: 1, Type: "chat.send", RequestID: randomID(), RoundID: c.roundID, ConnectionEpoch: epoch},
			SessionID: sessionID,
		},
		MessageID:     messageID,
		Seq:           strconv.FormatUint(counter, 10),
		NonceB64:      base64.StdEncoding.EncodeToString(nonce),
		CiphertextB64: base64.StdEncoding.EncodeToString(ciphertext),
	})
}

func setupSession(a, b *client, mode string) error {
	invite := &protocol.InviteCreate{
		Joined: protocol.Joined{V: 1, Type: "invite.create", RequestID: randomID(), RoundID: a.roundID, ConnectionEpoch: a.epoch},
		TargetID: b.id,
		Mode:     protocol.Mode(mode),
	}
	if err := a.send(invite); err != nil {
		return err
	}
	invited, err := waitSession(a, "INVITED")
	if err != nil {
		return err
	}
	if _, err := waitSession(b, "INVITED"); err != nil {
		return err
	}
	if err := b.send(&protocol.InviteRespond{
		SessionCommand: protocol.SessionCommand{
			Joined: protocol.Joined{V: 1, Type: "invite.respond", RequestID: randomID(), RoundID: b.roundID, ConnectionEpoch: b.epoch},
			SessionID: invited.SessionID,
		},
		Accept: boolPointer(true),
	}); err != nil {
		return err
	}
	state := "ACTIVE"
	if mode == "encrypted" {
		state = "NEGOTIATING"
	}
	accepted, err := waitSession(a, state)
	if err != nil {
		return err
	}
	if _, err := waitSession(b, state); err != nil {
		return err
	}
	if mode == "encrypted" {
		if err := encryptedHandshake(a, b, accepted.SessionID); err != nil {
			return err
		}
	}
	a.mu.Lock()
	a.sessionID = accepted.SessionID
	a.mode = mode
	a.role = "a"
	if mode == "encrypted" {
		a.counter = 1
	}
	a.mu.Unlock()
	b.mu.Lock()
	b.sessionID = accepted.SessionID
	b.mode = mode
	b.role = "b"
	if mode == "encrypted" {
		b.counter = 1
	}
	b.mu.Unlock()
	return nil
}

func encryptedHandshake(a, b *client, sessionID string) error {
	keyA := base64.StdEncoding.EncodeToString(bytesFill(32, 1))
	keyB := base64.StdEncoding.EncodeToString(bytesFill(32, 2))
	if err := a.send(&protocol.KeyPublish{
		SessionCommand: protocol.SessionCommand{
			Joined: protocol.Joined{V: 1, Type: "key.publish", RequestID: randomID(), RoundID: a.roundID, ConnectionEpoch: a.epoch},
			SessionID: sessionID,
		},
		PublicKeyB64: keyA,
	}); err != nil {
		return err
	}
	if err := b.send(&protocol.KeyPublish{
		SessionCommand: protocol.SessionCommand{
			Joined: protocol.Joined{V: 1, Type: "key.publish", RequestID: randomID(), RoundID: b.roundID, ConnectionEpoch: b.epoch},
			SessionID: sessionID,
		},
		PublicKeyB64: keyB,
	}); err != nil {
		return err
	}
	if err := waitKey(a); err != nil {
		return err
	}
	if err := waitKey(b); err != nil {
		return err
	}
	for _, pair := range []struct {
		c         *client
		direction byte
		key       string
	}{{a, 0, keyA}, {b, 1, keyB}} {
		nonce := make([]byte, 24)
		nonce[0] = pair.direction
		for i := 1; i < 16; i++ {
			nonce[i] = pair.direction + 1
		}
		if err := pair.c.send(&protocol.KeyConfirm{
			SessionCommand: protocol.SessionCommand{
				Joined: protocol.Joined{V: 1, Type: "key.confirm", RequestID: randomID(), RoundID: pair.c.roundID, ConnectionEpoch: pair.c.epoch},
				SessionID: sessionID,
			},
			Seq:           "0",
			NonceB64:      base64.StdEncoding.EncodeToString(nonce),
			CiphertextB64: base64.StdEncoding.EncodeToString(bytesFill(16, 3)),
		}); err != nil {
			return err
		}
	}
	if err := a.send(&protocol.KeyReady{
		SessionCommand: protocol.SessionCommand{
			Joined: protocol.Joined{V: 1, Type: "key.ready", RequestID: randomID(), RoundID: a.roundID, ConnectionEpoch: a.epoch},
			SessionID: sessionID,
		},
		Status: "ready",
	}); err != nil {
		return err
	}
	if err := b.send(&protocol.KeyReady{
		SessionCommand: protocol.SessionCommand{
			Joined: protocol.Joined{V: 1, Type: "key.ready", RequestID: randomID(), RoundID: b.roundID, ConnectionEpoch: b.epoch},
			SessionID: sessionID,
		},
		Status: "ready",
	}); err != nil {
		return err
	}
	if _, err := waitSession(a, "ACTIVE"); err != nil {
		return err
	}
	_, err := waitSession(b, "ACTIVE")
	return err
}

func waitSession(c *client, state string) (protocol.SessionView, error) {
	timeout := time.After(15 * time.Second)
	for {
		select {
		case session := <-c.sessionCh:
			if string(session.State) == state {
				return session, nil
			}
		case <-timeout:
			return protocol.SessionView{}, fmt.Errorf("client %d waiting for %s", c.index, state)
		}
	}
}

func waitKey(c *client) error {
	select {
	case <-c.peerKeyCh:
		return nil
	case <-time.After(15 * time.Second):
		return fmt.Errorf("client %d waiting for peer key", c.index)
	}
}

func fetchRoundID(wsURL string) (string, error) {
	httpURL := strings.Replace(wsURL, "ws://", "http://", 1)
	httpURL = strings.Replace(httpURL, "wss://", "https://", 1)
	httpURL = strings.TrimSuffix(httpURL, "/ws") + "/api/config"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, httpURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var config struct {
		Round struct {
			ID string `json:"id"`
		} `json:"round"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return "", err
	}
	if config.Round.ID == "" {
		return "", fmt.Errorf("empty round id")
	}
	return config.Round.ID, nil
}

func randomID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func bytesFill(length int, value byte) []byte {
	buf := make([]byte, length)
	for i := range buf {
		buf[i] = value
	}
	return buf
}

func boolPointer(value bool) *bool {
	return &value
}

func percentile(values []time.Duration, fraction float64) float64 {
	if len(values) == 0 {
		return 0
	}
	index := int(float64(len(values)-1) * fraction)
	return float64(values[index].Microseconds()) / 1000.0
}
