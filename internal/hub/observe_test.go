package hub

import (
	"context"
	"testing"
	"time"

	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/config"
	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/protocol"
)

func startHubWithAdmin(t *testing.T, cfg config.Config) (*Hub, string) {
	t.Helper()
	token, err := protocol.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
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
	return h, token
}

func authenticateTeacher(t *testing.T, h *Hub, token string) *fakePeer {
	t.Helper()
	epoch, peer := connectPeer(t, h, true)
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch, Message: &protocol.AdminAuth{
		V: 1, Type: "admin.auth", RequestID: newEpoch(t), Token: token,
	}}); err != nil {
		t.Fatalf("admin auth: %v", err)
	}
	processPending(t, h)
	_ = findMessage[*protocol.AdminWelcome](t, peer)
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch, Message: &protocol.ObserverSubscribe{
		V: 1, Type: "observer.subscribe", RequestID: newEpoch(t), SessionID: nil,
	}}); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	processPending(t, h)
	return peer
}

func subscribeObserver(t *testing.T, h *Hub, peer *fakePeer, sessionID string) *protocol.ObserverSnapshot {
	t.Helper()
	sid := sessionID
	if err := h.Submit(context.Background(), MessageCommand{Epoch: currentTeacherEpoch(peer), Message: &protocol.ObserverSubscribe{
		V: 1, Type: "observer.subscribe", RequestID: newEpoch(t), SessionID: &sid,
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	processPending(t, h)
	return lastObserverSnapshot(t, peer)
}

func currentTeacherEpoch(peer *fakePeer) string {
	for _, message := range peer.messages() {
		if welcome, ok := message.(*protocol.AdminWelcome); ok {
			return welcome.ConnectionEpoch
		}
	}
	return ""
}

func lastObserverSnapshot(t *testing.T, peer *fakePeer) *protocol.ObserverSnapshot {
	t.Helper()
	var last *protocol.ObserverSnapshot
	for _, message := range peer.messages() {
		if snapshot, ok := message.(*protocol.ObserverSnapshot); ok {
			last = snapshot
		}
	}
	if last == nil {
		t.Fatal("no observer.snapshot")
	}
	return last
}

func lastObserverEvent(peer *fakePeer) *protocol.ObserverEvent {
	var last *protocol.ObserverEvent
	for _, message := range peer.messages() {
		if event, ok := message.(*protocol.ObserverEvent); ok {
			last = event
		}
	}
	return last
}

func submitChatWS(t *testing.T, h *Hub, epoch, sessionID, messageID, seq string, text *string, wsBytes int) {
	t.Helper()
	msg := &protocol.ChatSend{
		SessionCommand: protocol.SessionCommand{
			Joined:    protocol.Joined{V: 1, Type: "chat.send", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: epoch},
			SessionID: sessionID,
		},
		MessageID: messageID,
		Seq:       seq,
		Text:      text,
	}
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch, Message: msg, WSBytes: wsBytes}); err != nil {
		t.Fatalf("chat: %v", err)
	}
	processPending(t, h)
	flushActivity(t, h)
}

func TestObserverSnapshotAndEvents(t *testing.T) {
	h, token := startHubWithAdmin(t, config.Default())
	fixture := setupPair(t, h)
	teacher := authenticateTeacher(t, h, token)
	sessionID := createPlaintextSession(t, h, fixture)

	empty := subscribeObserver(t, h, teacher, sessionID)
	if len(empty.Messages) != 0 || empty.SessionID != sessionID {
		t.Fatalf("empty snapshot = %+v", empty)
	}

	messageID := newEpoch(t)
	text := "观测消息"
	submitChatWS(t, h, fixture.aEpoch, sessionID, messageID, "0", &text, 321)
	event := lastObserverEvent(teacher)
	if event == nil {
		t.Fatal("missing observer event")
	}
	if event.Message.PayloadBytes != len([]byte(text)) {
		t.Fatalf("payload bytes = %d", event.Message.PayloadBytes)
	}
	if event.Message.WSMessageBytes != 321 {
		t.Fatalf("ws bytes = %d", event.Message.WSMessageBytes)
	}
	if event.Message.IntervalMS != nil {
		t.Fatalf("first interval = %v", *event.Message.IntervalMS)
	}
	if event.Message.Text == nil || *event.Message.Text != text {
		t.Fatalf("text = %v", event.Message.Text)
	}
	if event.Message.ReceiptStatus != "pending" {
		t.Fatalf("receipt = %q", event.Message.ReceiptStatus)
	}
	if event.Stats.MessageCount != 1 || event.Stats.PayloadBytesTotal != int64(len([]byte(text))) {
		t.Fatalf("stats = %+v", event.Stats)
	}

	receipt := &protocol.ChatReceipt{
		SessionCommand: protocol.SessionCommand{
			Joined:    protocol.Joined{V: 1, Type: "chat.receipt", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.bEpoch},
			SessionID: sessionID,
		},
		MessageID: messageID,
		Status:    "processed",
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.bEpoch, Message: receipt})
	processPending(t, h)
	event = lastObserverEvent(teacher)
	if event == nil || !event.Update || event.Message.ReceiptStatus != "confirmed" {
		t.Fatalf("update event = %+v", event)
	}
	if event.Stats.ConfirmedCount != 1 {
		t.Fatalf("confirmed stats = %+v", event.Stats)
	}

	submitChatWS(t, h, fixture.aEpoch, sessionID, messageID, "0", &text, 321)
	snapshot := subscribeObserver(t, h, teacher, sessionID)
	if snapshot.Stats.MessageCount != 1 || len(snapshot.Messages) != 1 {
		t.Fatalf("duplicate changed stats: %+v", snapshot)
	}
}

func TestObserverCanSwitchSessions(t *testing.T) {
	h, token := startHubWithAdmin(t, config.Default())
	fixture := setupPair(t, h)
	teacher := authenticateTeacher(t, h, token)
	first := createPlaintextSession(t, h, fixture)

	cEpoch, _, cWelcome := joinAs(t, h, "C")
	invite := &protocol.InviteCreate{
		Joined:   protocol.Joined{V: 1, Type: "invite.create", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.aEpoch},
		TargetID: cWelcome.ParticipantID, Mode: protocol.ModePlaintext,
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.aEpoch, Message: invite})
	processPending(t, h)
	second := lastSessionID(t, fixture.aPeer)
	accept := &protocol.InviteRespond{
		SessionCommand: protocol.SessionCommand{
			Joined:    protocol.Joined{V: 1, Type: "invite.respond", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: cEpoch},
			SessionID: second,
		},
		Accept: boolPtr(true),
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: cEpoch, Message: accept})
	processPending(t, h)

	text := "one"
	submitChatWS(t, h, fixture.aEpoch, first, newEpoch(t), "0", &text, 100)
	text2 := "two"
	submitChatWS(t, h, fixture.aEpoch, second, newEpoch(t), "0", &text2, 200)

	snapshot := subscribeObserver(t, h, teacher, first)
	if snapshot.SessionID != first || snapshot.Stats.MessageCount != 1 {
		t.Fatalf("first snapshot = %+v", snapshot)
	}
	snapshot = subscribeObserver(t, h, teacher, second)
	if snapshot.SessionID != second || snapshot.Stats.MessageCount != 1 {
		t.Fatalf("second snapshot = %+v", snapshot)
	}
}

func TestStudentCannotSubscribeObservations(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	fixture := setupPair(t, h)
	sessionID := createPlaintextSession(t, h, fixture)
	sid := sessionID
	if err := h.Submit(context.Background(), MessageCommand{Epoch: fixture.aEpoch, Message: &protocol.ObserverSubscribe{
		V: 1, Type: "observer.subscribe", RequestID: newEpoch(t), SessionID: &sid,
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	processPending(t, h)
	if err := findLastError(fixture.aPeer); err == nil || err.Code != protocol.CodeForbidden {
		t.Fatalf("error = %+v", err)
	}
}

func TestObserverUnconfirmedAndEncryptedPayload(t *testing.T) {
	h, token := startHubWithAdmin(t, config.Default())
	fixture := setupPair(t, h)
	teacher := authenticateTeacher(t, h, token)
	sessionID := activateEncryptedSession(t, h, fixture)
	subscribeObserver(t, h, teacher, sessionID)

	messageID := newEpoch(t)
	chat := &protocol.ChatSend{
		SessionCommand: protocol.SessionCommand{
			Joined:    protocol.Joined{V: 1, Type: "chat.send", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.aEpoch},
			SessionID: sessionID,
		},
		MessageID:     messageID,
		Seq:           "1",
		NonceB64:      b64Bytes(encryptedNonce(0, 1, 0x66)),
		CiphertextB64: b64Bytes(make([]byte, 48)),
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.aEpoch, Message: chat, WSBytes: 500})
	processPending(t, h)
	event := lastObserverEvent(teacher)
	if event == nil || event.Message.PayloadBytes != 48 || event.Message.WSMessageBytes != 500 {
		t.Fatalf("encrypted event = %+v", event)
	}
	if event.Message.Text != nil || event.Message.CiphertextB64 == nil || event.Message.NonceB64 == nil {
		t.Fatalf("encrypted payload = %+v", event.Message)
	}

	_ = h.Submit(context.Background(), TickCommand{Now: fixedTime().Add(6 * time.Second)})
	processPending(t, h)
	event = lastObserverEvent(teacher)
	if event == nil || event.Message.ReceiptStatus != "unconfirmed" {
		t.Fatalf("unconfirmed event = %+v", event)
	}
}
