package hub

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/config"
	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/protocol"
)

func (p *fakePeer) messages() []any {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]any, len(p.sent))
	copy(out, p.sent)
	return out
}

func connectPeer(t *testing.T, h *Hub, admin bool) (string, *fakePeer) {
	t.Helper()
	epoch := newEpoch(t)
	peer := &fakePeer{}
	conn := NewConnection(epoch, fixedTime())
	conn.Admin = admin
	conn.Peer = peer
	reply := make(chan error, 1)
	if err := h.Submit(context.Background(), ConnectCommand{Conn: conn, Reply: reply}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := <-reply; err != nil {
		t.Fatalf("connect error = %v", err)
	}
	return epoch, peer
}

func processPending(t *testing.T, h *Hub) {
	t.Helper()
	if _, err := h.Snapshot(context.Background()); err != nil {
		t.Fatalf("snapshot barrier: %v", err)
	}
}

func findMessage[T any](t *testing.T, peer *fakePeer) T {
	t.Helper()
	for _, message := range peer.messages() {
		if typed, ok := message.(T); ok {
			return typed
		}
	}
	var zero T
	t.Fatalf("message %T not found in %v", zero, peer.messages())
	return zero
}

func joinMessage(t *testing.T, h *Hub, nickname string) *protocol.Join {
	t.Helper()
	return &protocol.Join{
		V:         1,
		Type:      "join",
		RequestID: newEpoch(t),
		RoundID:   h.RoundID(),
		Nickname:  nickname,
	}
}

func TestJoinSendsWelcomeAndTopology(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	epoch, peer := connectPeer(t, h, false)
	join := joinMessage(t, h, "小明")
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch, Message: join}); err != nil {
		t.Fatalf("join submit: %v", err)
	}
	processPending(t, h)

	welcome := findMessage[*protocol.Welcome](t, peer)
	if welcome.RequestID != join.RequestID {
		t.Errorf("request id = %q", welcome.RequestID)
	}
	if welcome.Nickname != "小明" {
		t.Errorf("nickname = %q", welcome.Nickname)
	}
	if !protocol.ValidID(welcome.ParticipantID) {
		t.Errorf("participant id = %q", welcome.ParticipantID)
	}
	if len(welcome.ShortID) != 4 || strings.ToUpper(welcome.ShortID) != welcome.ShortID {
		t.Errorf("short id = %q", welcome.ShortID)
	}
	if !strings.HasPrefix(welcome.Color, "#") || len(welcome.Color) != 7 {
		t.Errorf("color = %q", welcome.Color)
	}
	if !protocol.ValidToken(welcome.ResumeToken) {
		t.Errorf("resume token = %q", welcome.ResumeToken)
	}
	if welcome.ConnectionEpoch != epoch {
		t.Errorf("connection epoch = %q", welcome.ConnectionEpoch)
	}
	if welcome.Round.ID != h.RoundID() || welcome.Round.Status != "RUNNING" {
		t.Errorf("round = %+v", welcome.Round)
	}
	if len(welcome.Participants) != 1 || welcome.Participants[0].ID != welcome.ParticipantID {
		t.Errorf("participants = %+v", welcome.Participants)
	}
	if !welcome.Participants[0].Online {
		t.Error("joined participant is not online")
	}

	topology := findMessage[*protocol.TopologyChanged](t, peer)
	if topology.ConnectionEpoch != epoch {
		t.Errorf("topology epoch = %q", topology.ConnectionEpoch)
	}
	if topology.TopologyVersion < welcome.TopologyVersion {
		t.Errorf("topology version went backwards: %d < %d", topology.TopologyVersion, welcome.TopologyVersion)
	}
	if len(topology.Participants) != 1 {
		t.Errorf("topology participants = %+v", topology.Participants)
	}
}

func TestJoinAllowsDuplicateNicknamesWithDistinctIDs(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	epoch1, peer1 := connectPeer(t, h, false)
	epoch2, peer2 := connectPeer(t, h, false)
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch1, Message: joinMessage(t, h, "同名")}); err != nil {
		t.Fatalf("join 1: %v", err)
	}
	processPending(t, h)
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch2, Message: joinMessage(t, h, "同名")}); err != nil {
		t.Fatalf("join 2: %v", err)
	}
	processPending(t, h)

	welcome1 := findMessage[*protocol.Welcome](t, peer1)
	welcome2 := findMessage[*protocol.Welcome](t, peer2)
	if welcome1.ParticipantID == welcome2.ParticipantID {
		t.Fatal("duplicate participant ids")
	}
	if welcome1.ShortID == welcome2.ShortID {
		t.Fatalf("duplicate short ids: %q", welcome1.ShortID)
	}
	if welcome1.Color == welcome2.Color {
		t.Fatalf("duplicate colors: %q", welcome1.Color)
	}
	if welcome1.ResumeToken == welcome2.ResumeToken {
		t.Fatal("duplicate resume tokens")
	}

	topology := lastTopology(t, peer1)
	if len(topology.Participants) != 2 {
		t.Fatalf("participants = %+v", topology.Participants)
	}
}

func lastTopology(t *testing.T, peer *fakePeer) *protocol.TopologyChanged {
	t.Helper()
	var last *protocol.TopologyChanged
	for _, message := range peer.messages() {
		if topology, ok := message.(*protocol.TopologyChanged); ok {
			last = topology
		}
	}
	if last == nil {
		t.Fatal("no topology message")
	}
	return last
}

func TestWelcomeDoesNotLeakTokensIntoTopology(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	epoch, peer := connectPeer(t, h, false)
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch, Message: joinMessage(t, h, "小明")}); err != nil {
		t.Fatalf("join: %v", err)
	}
	processPending(t, h)
	welcome := findMessage[*protocol.Welcome](t, peer)
	topology := findMessage[*protocol.TopologyChanged](t, peer)
	data, err := json.Marshal(topology)
	if err != nil {
		t.Fatalf("marshal topology: %v", err)
	}
	if strings.Contains(string(data), welcome.ResumeToken) {
		t.Fatal("topology contains resume token")
	}
	if strings.Contains(string(data), "resume") || strings.Contains(string(data), "private") {
		t.Fatalf("topology contains secret-like field: %s", data)
	}
}

func TestTopologyBroadcastOnSecondJoinAndDisconnect(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	epoch1, peer1 := connectPeer(t, h, false)
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch1, Message: joinMessage(t, h, "A")}); err != nil {
		t.Fatalf("join 1: %v", err)
	}
	processPending(t, h)

	epoch2, _ := connectPeer(t, h, false)
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch2, Message: joinMessage(t, h, "B")}); err != nil {
		t.Fatalf("join 2: %v", err)
	}
	processPending(t, h)

	var last *protocol.TopologyChanged
	for _, message := range peer1.messages() {
		if topology, ok := message.(*protocol.TopologyChanged); ok {
			last = topology
		}
	}
	if last == nil || len(last.Participants) != 2 {
		t.Fatalf("last topology = %+v", last)
	}

	if err := h.Submit(context.Background(), DisconnectCommand{Epoch: epoch2}); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	processPending(t, h)
	last = nil
	for _, message := range peer1.messages() {
		if topology, ok := message.(*protocol.TopologyChanged); ok {
			last = topology
		}
	}
	if last == nil || len(last.Participants) != 1 {
		t.Fatalf("topology after disconnect = %+v", last)
	}
}

func TestAdminAuth(t *testing.T) {
	token, err := protocol.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	h, err := newHubWithOptions(config.Default(), testLogger(), func() time.Time { return fixedTime() }, Options{
		AdminToken:    token,
		ServerVersion: "test-version",
	})
	if err != nil {
		t.Fatalf("newHub: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go h.Run(ctx)
	t.Cleanup(func() {
		cancel()
		<-h.Done()
	})

	epoch, peer := connectPeer(t, h, true)
	requestID := newEpoch(t)
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch, Message: &protocol.AdminAuth{
		V:         1,
		Type:      "admin.auth",
		RequestID: requestID,
		Token:     token,
	}}); err != nil {
		t.Fatalf("admin auth: %v", err)
	}
	processPending(t, h)
	welcome := findMessage[*protocol.AdminWelcome](t, peer)
	if welcome.RequestID != requestID {
		t.Errorf("request id = %q", welcome.RequestID)
	}
	if welcome.ServerVersion != "test-version" {
		t.Errorf("server version = %q", welcome.ServerVersion)
	}
	if welcome.ConnectionEpoch != epoch {
		t.Errorf("epoch = %q", welcome.ConnectionEpoch)
	}
	if welcome.Round.ID != h.RoundID() {
		t.Errorf("round = %+v", welcome.Round)
	}
}

func TestAdminAuthRejectsWrongToken(t *testing.T) {
	token, _ := protocol.NewToken()
	h, err := newHubWithOptions(config.Default(), testLogger(), func() time.Time { return fixedTime() }, Options{AdminToken: token})
	if err != nil {
		t.Fatalf("newHub: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go h.Run(ctx)
	t.Cleanup(func() {
		cancel()
		<-h.Done()
	})

	epoch, peer := connectPeer(t, h, true)
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch, Message: &protocol.AdminAuth{
		V:         1,
		Type:      "admin.auth",
		RequestID: newEpoch(t),
		Token:     "invalid",
	}}); err != nil {
		t.Fatalf("admin auth: %v", err)
	}
	processPending(t, h)
	errorMessage := findMessage[*protocol.ErrorMessage](t, peer)
	if errorMessage.Code != protocol.CodeUnauthorized {
		t.Fatalf("code = %s", errorMessage.Code)
	}
	if !peer.isClosed() {
		t.Fatal("unauthorized peer not closed")
	}
}

func TestPublicConnectionCannotAuthenticateAsAdmin(t *testing.T) {
	token, _ := protocol.NewToken()
	h, err := newHubWithOptions(config.Default(), testLogger(), func() time.Time { return fixedTime() }, Options{AdminToken: token})
	if err != nil {
		t.Fatalf("newHub: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go h.Run(ctx)
	t.Cleanup(func() {
		cancel()
		<-h.Done()
	})

	epoch, peer := connectPeer(t, h, false)
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch, Message: &protocol.AdminAuth{
		V:         1,
		Type:      "admin.auth",
		RequestID: newEpoch(t),
		Token:     token,
	}}); err != nil {
		t.Fatalf("admin auth: %v", err)
	}
	processPending(t, h)
	errorMessage := findMessage[*protocol.ErrorMessage](t, peer)
	if errorMessage.Code != protocol.CodeForbidden {
		t.Fatalf("code = %s", errorMessage.Code)
	}
}

func TestJoinCapacity(t *testing.T) {
	cfg := config.Default()
	cfg.MaxParticipants = 1
	cfg.MaxIdentities = 2
	h, err := newHub(cfg, testLogger(), func() time.Time { return fixedTime() })
	if err != nil {
		t.Fatalf("newHub: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go h.Run(ctx)
	t.Cleanup(func() {
		cancel()
		<-h.Done()
	})

	epoch1, _ := connectPeer(t, h, false)
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch1, Message: joinMessage(t, h, "A")}); err != nil {
		t.Fatalf("join 1: %v", err)
	}
	processPending(t, h)

	epoch2, peer2 := connectPeer(t, h, false)
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch2, Message: joinMessage(t, h, "B")}); err != nil {
		t.Fatalf("join 2: %v", err)
	}
	processPending(t, h)
	errorMessage := findMessage[*protocol.ErrorMessage](t, peer2)
	if errorMessage.Code != protocol.CodeCapacityReached {
		t.Fatalf("code = %s", errorMessage.Code)
	}

	snapshot, _ := h.Snapshot(context.Background())
	if len(snapshot.Participants) != 1 {
		t.Fatalf("participants = %d", len(snapshot.Participants))
	}
}

func TestJoinRejectsStaleRoundAndAdminConnection(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })

	epoch, peer := connectPeer(t, h, false)
	stale := joinMessage(t, h, "A")
	stale.RoundID = "11111111111111111111111111111111"
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch, Message: stale}); err != nil {
		t.Fatalf("join: %v", err)
	}
	processPending(t, h)
	errorMessage := findMessage[*protocol.ErrorMessage](t, peer)
	if errorMessage.Code != protocol.CodeStaleRound {
		t.Fatalf("code = %s", errorMessage.Code)
	}

	adminEpoch, adminPeer := connectPeer(t, h, true)
	if err := h.Submit(context.Background(), MessageCommand{Epoch: adminEpoch, Message: joinMessage(t, h, "B")}); err != nil {
		t.Fatalf("admin join: %v", err)
	}
	processPending(t, h)
	errorMessage = findMessage[*protocol.ErrorMessage](t, adminPeer)
	if errorMessage.Code != protocol.CodeForbidden {
		t.Fatalf("code = %s", errorMessage.Code)
	}
}

func TestPingReturnsPong(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	epoch, peer := connectPeer(t, h, false)
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch, Message: &protocol.Ping{V: 1, Type: "ping"}}); err != nil {
		t.Fatalf("ping: %v", err)
	}
	processPending(t, h)
	pong := findMessage[*protocol.Pong](t, peer)
	if pong.Type != "pong" || pong.ServerTimeUTC == "" {
		t.Fatalf("pong = %+v", pong)
	}
}

type setEpochCommand struct {
	participantID string
	epoch         string
}

func (c setEpochCommand) apply(h *Hub) {
	if participant, ok := h.state.participants[c.participantID]; ok {
		participant.ConnectionEpoch = c.epoch
	}
}

func TestStaleEpochAndRoundAreRejected(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	epoch, peer := connectPeer(t, h, false)
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch, Message: joinMessage(t, h, "A")}); err != nil {
		t.Fatalf("join: %v", err)
	}
	processPending(t, h)
	welcome := findMessage[*protocol.Welcome](t, peer)

	if err := h.Submit(context.Background(), setEpochCommand{participantID: welcome.ParticipantID, epoch: "22222222222222222222222222222222"}); err != nil {
		t.Fatalf("set epoch: %v", err)
	}
	chat := &protocol.ChatSend{
		SessionCommand: protocol.SessionCommand{
			Joined: protocol.Joined{
				V:               1,
				Type:            "chat.send",
				RequestID:       newEpoch(t),
				RoundID:         h.RoundID(),
				ConnectionEpoch: epoch,
			},
			SessionID: "33333333333333333333333333333333",
		},
		MessageID: "44444444444444444444444444444444",
		Seq:       "0",
		Text:      stringPointer("hello"),
	}
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch, Message: chat}); err != nil {
		t.Fatalf("chat: %v", err)
	}
	processPending(t, h)
	errorMessage := findLastError(peer)
	if errorMessage == nil || errorMessage.Code != protocol.CodeStaleConnection {
		t.Fatalf("error = %+v", errorMessage)
	}

	if err := h.Submit(context.Background(), setEpochCommand{participantID: welcome.ParticipantID, epoch: epoch}); err != nil {
		t.Fatalf("restore epoch: %v", err)
	}
	staleChat := *chat
	staleChat.RequestID = newEpoch(t)
	staleChat.RoundID = "55555555555555555555555555555555"
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch, Message: &staleChat}); err != nil {
		t.Fatalf("stale round: %v", err)
	}
	processPending(t, h)
	errorMessage = findLastError(peer)
	if errorMessage == nil || errorMessage.Code != protocol.CodeStaleRound {
		t.Fatalf("error = %+v", errorMessage)
	}
}

func findLastError(peer *fakePeer) *protocol.ErrorMessage {
	var last *protocol.ErrorMessage
	for _, message := range peer.messages() {
		if errorMessage, ok := message.(*protocol.ErrorMessage); ok {
			last = errorMessage
		}
	}
	return last
}

func stringPointer(value string) *string {
	return &value
}

func TestOldEpochDisconnectDoesNotAffectNewConnection(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	epoch1, peer1 := connectPeer(t, h, false)
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch1, Message: joinMessage(t, h, "A")}); err != nil {
		t.Fatalf("join: %v", err)
	}
	processPending(t, h)
	welcome := findMessage[*protocol.Welcome](t, peer1)

	epoch2 := newEpoch(t)
	peer2 := &fakePeer{}
	conn2 := NewConnection(epoch2, fixedTime())
	conn2.Peer = peer2
	conn2.Bind(welcome.ParticipantID)
	reply := make(chan error, 1)
	if err := h.Submit(context.Background(), ConnectCommand{Conn: conn2, Reply: reply}); err != nil {
		t.Fatalf("connect 2: %v", err)
	}
	if err := <-reply; err != nil {
		t.Fatalf("connect 2 error = %v", err)
	}

	if err := h.Submit(context.Background(), DisconnectCommand{Epoch: epoch1}); err != nil {
		t.Fatalf("disconnect old: %v", err)
	}
	processPending(t, h)
	snapshot, _ := h.Snapshot(context.Background())
	if len(snapshot.Participants) != 1 {
		t.Fatalf("participants = %d", len(snapshot.Participants))
	}
	if snapshot.Participants[0].State != ParticipantOnline {
		t.Fatalf("participant state = %q", snapshot.Participants[0].State)
	}
	if snapshot.Participants[0].ConnectionEpoch != epoch2 {
		t.Fatalf("participant epoch = %q, want %q", snapshot.Participants[0].ConnectionEpoch, epoch2)
	}
}

func TestIdentityCapacity(t *testing.T) {
	cfg := config.Default()
	cfg.MaxParticipants = 10
	cfg.MaxIdentities = 1
	h, err := newHub(cfg, testLogger(), func() time.Time { return fixedTime() })
	if err != nil {
		t.Fatalf("newHub: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go h.Run(ctx)
	t.Cleanup(func() {
		cancel()
		<-h.Done()
	})

	epoch1, _ := connectPeer(t, h, false)
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch1, Message: joinMessage(t, h, "A")}); err != nil {
		t.Fatalf("join 1: %v", err)
	}
	processPending(t, h)

	epoch2, peer2 := connectPeer(t, h, false)
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch2, Message: joinMessage(t, h, "B")}); err != nil {
		t.Fatalf("join 2: %v", err)
	}
	processPending(t, h)
	errorMessage := findMessage[*protocol.ErrorMessage](t, peer2)
	if errorMessage.Code != protocol.CodeCapacityReached {
		t.Fatalf("code = %s", errorMessage.Code)
	}
}

func TestTeacherCapacity(t *testing.T) {
	token, _ := protocol.NewToken()
	cfg := config.Default()
	cfg.MaxTeachers = 1
	h, err := newHubWithOptions(cfg, testLogger(), func() time.Time { return fixedTime() }, Options{AdminToken: token})
	if err != nil {
		t.Fatalf("newHub: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go h.Run(ctx)
	t.Cleanup(func() {
		cancel()
		<-h.Done()
	})

	epoch1, _ := connectPeer(t, h, true)
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch1, Message: &protocol.AdminAuth{V: 1, Type: "admin.auth", RequestID: newEpoch(t), Token: token}}); err != nil {
		t.Fatalf("auth 1: %v", err)
	}
	processPending(t, h)

	epoch2, peer2 := connectPeer(t, h, true)
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch2, Message: &protocol.AdminAuth{V: 1, Type: "admin.auth", RequestID: newEpoch(t), Token: token}}); err != nil {
		t.Fatalf("auth 2: %v", err)
	}
	processPending(t, h)
	errorMessage := findMessage[*protocol.ErrorMessage](t, peer2)
	if errorMessage.Code != protocol.CodeCapacityReached {
		t.Fatalf("code = %s", errorMessage.Code)
	}
}
