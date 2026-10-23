package hub

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/config"
	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/protocol"
)

type fakePeer struct {
	mu      sync.Mutex
	closed  bool
	sent    []any
	reason  string
	sendErr error
}

func (p *fakePeer) Send(message any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sendErr != nil {
		return p.sendErr
	}
	p.sent = append(p.sent, message)
	return nil
}

func (p *fakePeer) Close(reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.reason = reason
}

func (p *fakePeer) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func startTestHub(t *testing.T, now func() time.Time) (*Hub, context.CancelFunc) {
	t.Helper()
	h, err := newHub(config.Default(), testLogger(), now)
	if err != nil {
		t.Fatalf("newHub() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go h.Run(ctx)
	t.Cleanup(func() {
		cancel()
		<-h.Done()
	})
	return h, cancel
}

func newEpoch(t *testing.T) string {
	t.Helper()
	id, err := protocol.NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	return id
}

type addParticipantCommand struct {
	participant Participant
}

func (c addParticipantCommand) apply(h *Hub) {
	copy := c.participant
	h.state.participants[copy.ID] = &copy
	h.state.topologyVersion++
}

func TestHubConcurrentConnectsAreSerialized(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	const total = 100
	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn := NewConnection(newEpoch(t), fixedTime())
			reply := make(chan error, 1)
			if err := h.Submit(context.Background(), ConnectCommand{Conn: conn, Reply: reply}); err != nil {
				t.Errorf("Submit() error = %v", err)
				return
			}
			if err := <-reply; err != nil {
				t.Errorf("Connect error = %v", err)
			}
		}()
	}
	wg.Wait()

	snapshot, err := h.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(snapshot.Connections) != total {
		t.Fatalf("connections = %d, want %d", len(snapshot.Connections), total)
	}
	seen := make(map[string]bool, total)
	for _, conn := range snapshot.Connections {
		if seen[conn.Epoch] {
			t.Fatalf("duplicate epoch %q in snapshot", conn.Epoch)
		}
		seen[conn.Epoch] = true
	}
	if snapshot.TopologyVersion < total {
		t.Fatalf("topology version = %d, want >= %d", snapshot.TopologyVersion, total)
	}
	if snapshot.Round.Status != RoundRunning {
		t.Fatalf("round status = %q", snapshot.Round.Status)
	}
}

func TestHubRejectsDuplicateEpoch(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	epoch := newEpoch(t)
	first := make(chan error, 1)
	if err := h.Submit(context.Background(), ConnectCommand{Conn: NewConnection(epoch, fixedTime()), Reply: first}); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if err := <-first; err != nil {
		t.Fatalf("first connect error = %v", err)
	}
	second := make(chan error, 1)
	if err := h.Submit(context.Background(), ConnectCommand{Conn: NewConnection(epoch, fixedTime()), Reply: second}); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if err := <-second; err == nil {
		t.Fatal("duplicate epoch accepted")
	}

	snapshot, _ := h.Snapshot(context.Background())
	if len(snapshot.Connections) != 1 {
		t.Fatalf("connections = %d", len(snapshot.Connections))
	}
}

func TestHubDisconnectMarksParticipantOffline(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	participantID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	epoch := newEpoch(t)

	if err := h.Submit(context.Background(), addParticipantCommand{participant: NewParticipant(participantID, "小明", "AAAA", "#2563EB", fixedTime())}); err != nil {
		t.Fatalf("add participant: %v", err)
	}
	conn := NewConnection(epoch, fixedTime())
	conn.Bind(participantID)
	reply := make(chan error, 1)
	if err := h.Submit(context.Background(), ConnectCommand{Conn: conn, Reply: reply}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := <-reply; err != nil {
		t.Fatalf("connect error = %v", err)
	}

	disconnectReply := make(chan error, 1)
	if err := h.Submit(context.Background(), DisconnectCommand{Epoch: epoch, Reply: disconnectReply}); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if err := <-disconnectReply; err != nil {
		t.Fatalf("disconnect error = %v", err)
	}

	snapshot, _ := h.Snapshot(context.Background())
	if len(snapshot.Connections) != 0 {
		t.Fatalf("connections = %d", len(snapshot.Connections))
	}
	if len(snapshot.Participants) != 1 {
		t.Fatalf("participants = %d", len(snapshot.Participants))
	}
	if snapshot.Participants[0].State != ParticipantOffline {
		t.Fatalf("participant state = %q", snapshot.Participants[0].State)
	}
}

func TestHubTouchUpdatesLastSeen(t *testing.T) {
	base := fixedTime()
	h, _ := startTestHub(t, func() time.Time { return base })
	epoch := newEpoch(t)
	reply := make(chan error, 1)
	if err := h.Submit(context.Background(), ConnectCommand{Conn: NewConnection(epoch, base), Reply: reply}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := <-reply; err != nil {
		t.Fatalf("connect error = %v", err)
	}

	later := base.Add(15 * time.Second)
	if err := h.Submit(context.Background(), TouchCommand{Epoch: epoch, At: later}); err != nil {
		t.Fatalf("touch: %v", err)
	}
	snapshot, _ := h.Snapshot(context.Background())
	if len(snapshot.Connections) != 1 {
		t.Fatalf("connections = %d", len(snapshot.Connections))
	}
	if !snapshot.Connections[0].LastSeen.Equal(later) {
		t.Fatalf("last seen = %v, want %v", snapshot.Connections[0].LastSeen, later)
	}
}

func TestHubSnapshotIsolation(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	participantID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := h.Submit(context.Background(), addParticipantCommand{participant: NewParticipant(participantID, "小明", "AAAA", "#2563EB", fixedTime())}); err != nil {
		t.Fatalf("add participant: %v", err)
	}
	snapshot, err := h.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	snapshot.Participants[0].Nickname = "changed"
	snapshot.Round.Status = RoundEnded

	again, err := h.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if again.Participants[0].Nickname != "小明" {
		t.Fatalf("internal participant changed to %q", again.Participants[0].Nickname)
	}
	if again.Round.Status != RoundRunning {
		t.Fatalf("internal round changed to %q", again.Round.Status)
	}
}

func TestHubShutdownClosesPeers(t *testing.T) {
	h, err := newHub(config.Default(), testLogger(), func() time.Time { return fixedTime() })
	if err != nil {
		t.Fatalf("newHub() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go h.Run(ctx)

	peer := &fakePeer{}
	conn := NewConnection(newEpoch(t), fixedTime())
	conn.Peer = peer
	reply := make(chan error, 1)
	if err := h.Submit(ctx, ConnectCommand{Conn: conn, Reply: reply}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := <-reply; err != nil {
		t.Fatalf("connect error = %v", err)
	}

	cancel()
	select {
	case <-h.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("hub did not stop")
	}
	if !peer.isClosed() {
		t.Fatal("peer not closed on shutdown")
	}
	if err := h.Submit(context.Background(), TouchCommand{Epoch: conn.Epoch, At: fixedTime()}); err == nil {
		t.Fatal("Submit after shutdown succeeded")
	}
}

func TestHubSnapshotIsConsistentUnderConcurrentConnects(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	const total = 50
	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn := NewConnection(newEpoch(t), fixedTime())
			reply := make(chan error, 1)
			if err := h.Submit(context.Background(), ConnectCommand{Conn: conn, Reply: reply}); err != nil {
				t.Errorf("Submit() error = %v", err)
				return
			}
			<-reply
		}()
	}

	var snapshots []Snapshot
	var snapMu sync.Mutex
	var snapWG sync.WaitGroup
	for i := 0; i < 20; i++ {
		snapWG.Add(1)
		go func() {
			defer snapWG.Done()
			snapshot, err := h.Snapshot(context.Background())
			if err != nil {
				t.Errorf("Snapshot() error = %v", err)
				return
			}
			snapMu.Lock()
			snapshots = append(snapshots, snapshot)
			snapMu.Unlock()
		}()
	}
	wg.Wait()
	snapWG.Wait()

	for _, snapshot := range snapshots {
		seen := make(map[string]bool, len(snapshot.Connections))
		for _, conn := range snapshot.Connections {
			if seen[conn.Epoch] {
				t.Fatalf("snapshot has duplicate epoch %q", conn.Epoch)
			}
			seen[conn.Epoch] = true
		}
		if len(snapshot.Connections) > total {
			t.Fatalf("snapshot has %d connections, want <= %d", len(snapshot.Connections), total)
		}
	}
}
