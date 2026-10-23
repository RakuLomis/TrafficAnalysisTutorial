package hub

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/config"
	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/protocol"
)

type pairFixture struct {
	aEpoch string
	bEpoch string
	aPeer  *fakePeer
	bPeer  *fakePeer
	aID    string
	bID    string
	aShort string
	bShort string
}

func joinAs(t *testing.T, h *Hub, nickname string) (string, *fakePeer, *protocol.Welcome) {
	t.Helper()
	epoch, peer := connectPeer(t, h, false)
	join := joinMessage(t, h, nickname)
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch, Message: join}); err != nil {
		t.Fatalf("join %s: %v", nickname, err)
	}
	processPending(t, h)
	welcome := findMessage[*protocol.Welcome](t, peer)
	return epoch, peer, welcome
}

func setupPair(t *testing.T, h *Hub) pairFixture {
	t.Helper()
	aEpoch, aPeer, aWelcome := joinAs(t, h, "A")
	bEpoch, bPeer, bWelcome := joinAs(t, h, "B")
	return pairFixture{
		aEpoch: aEpoch,
		bEpoch: bEpoch,
		aPeer:  aPeer,
		bPeer:  bPeer,
		aID:    aWelcome.ParticipantID,
		bID:    bWelcome.ParticipantID,
		aShort: aWelcome.ShortID,
		bShort: bWelcome.ShortID,
	}
}

func lastSessionChangedFor(t *testing.T, peer *fakePeer, sessionID string) *protocol.SessionChanged {
	t.Helper()
	var last *protocol.SessionChanged
	for _, message := range peer.messages() {
		changed, ok := message.(*protocol.SessionChanged)
		if !ok || changed.Session.SessionID != sessionID {
			continue
		}
		last = changed
	}
	if last == nil {
		t.Fatalf("no session.changed for %s in %v", sessionID, peer.messages())
	}
	return last
}

func lastSessionID(t *testing.T, peer *fakePeer) string {
	t.Helper()
	var last *protocol.SessionChanged
	for _, message := range peer.messages() {
		if changed, ok := message.(*protocol.SessionChanged); ok {
			last = changed
		}
	}
	if last == nil {
		t.Fatal("no session.changed")
	}
	return last.Session.SessionID
}

func submitChat(t *testing.T, h *Hub, epoch, sessionID, messageID, seq string, text *string) {
	t.Helper()
	msg := &protocol.ChatSend{
		SessionCommand: protocol.SessionCommand{
			Joined: protocol.Joined{
				V:               1,
				Type:            "chat.send",
				RequestID:       newEpoch(t),
				RoundID:         h.RoundID(),
				ConnectionEpoch: epoch,
			},
			SessionID: sessionID,
		},
		MessageID: messageID,
		Seq:       seq,
		Text:      text,
	}
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch, Message: msg}); err != nil {
		t.Fatalf("chat.send: %v", err)
	}
	processPending(t, h)
	flushActivity(t, h)
}

type activityCommand struct {
	event protocol.ActivityEvent
}

func (c activityCommand) apply(h *Hub) {
	h.broadcastActivity(c.event)
}

func flushActivity(t *testing.T, h *Hub) {
	t.Helper()
	if err := h.Submit(context.Background(), flushActivityCommand{}); err != nil {
		t.Fatalf("flush activity: %v", err)
	}
	processPending(t, h)
}

func textPtr(value string) *string {
	return &value
}

func createPlaintextSession(t *testing.T, h *Hub, fixture pairFixture) string {
	t.Helper()
	invite := &protocol.InviteCreate{
		Joined: protocol.Joined{
			V:               1,
			Type:            "invite.create",
			RequestID:       newEpoch(t),
			RoundID:         h.RoundID(),
			ConnectionEpoch: fixture.aEpoch,
		},
		TargetID: fixture.bID,
		Mode:     protocol.ModePlaintext,
	}
	if err := h.Submit(context.Background(), MessageCommand{Epoch: fixture.aEpoch, Message: invite}); err != nil {
		t.Fatalf("invite: %v", err)
	}
	processPending(t, h)
	sessionID := lastSessionID(t, fixture.aPeer)
	if state := lastSessionChangedFor(t, fixture.aPeer, sessionID).Session.State; state != string(SessionInvited) {
		t.Fatalf("invite state = %s", state)
	}

	accept := &protocol.InviteRespond{
		SessionCommand: protocol.SessionCommand{
			Joined: protocol.Joined{
				V:               1,
				Type:            "invite.respond",
				RequestID:       newEpoch(t),
				RoundID:         h.RoundID(),
				ConnectionEpoch: fixture.bEpoch,
			},
			SessionID: sessionID,
		},
		Accept: boolPtr(true),
	}
	if err := h.Submit(context.Background(), MessageCommand{Epoch: fixture.bEpoch, Message: accept}); err != nil {
		t.Fatalf("accept: %v", err)
	}
	processPending(t, h)
	if state := lastSessionChangedFor(t, fixture.aPeer, sessionID).Session.State; state != string(SessionActive) {
		t.Fatalf("accept state = %s", state)
	}
	return sessionID
}

func boolPtr(value bool) *bool {
	return &value
}

func lastChatStatus(peer *fakePeer) *protocol.ChatStatus {
	var last *protocol.ChatStatus
	for _, message := range peer.messages() {
		if status, ok := message.(*protocol.ChatStatus); ok {
			last = status
		}
	}
	return last
}

func countMessages[T any](peer *fakePeer) int {
	count := 0
	for _, message := range peer.messages() {
		if _, ok := message.(T); ok {
			count++
		}
	}
	return count
}

func TestPlaintextInviteChatReceiptAndDedup(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	fixture := setupPair(t, h)
	sessionID := createPlaintextSession(t, h, fixture)

	messageID := newEpoch(t)
	submitChat(t, h, fixture.aEpoch, sessionID, messageID, "0", textPtr("你好"))

	accepted := findMessage[*protocol.ChatAccepted](t, fixture.aPeer)
	if accepted.SessionID != sessionID || accepted.MessageID != messageID {
		t.Fatalf("accepted = %+v", accepted)
	}
	deliver := findMessage[*protocol.ChatDeliver](t, fixture.bPeer)
	if deliver.Text == nil || *deliver.Text != "你好" {
		t.Fatalf("deliver = %+v", deliver)
	}
	if deliver.SenderID != fixture.aID {
		t.Fatalf("deliver sender = %q", deliver.SenderID)
	}
	activity := findMessage[*protocol.ActivityBatch](t, fixture.bPeer)
	if len(activity.Events) != 1 {
		t.Fatalf("activity events = %+v", activity.Events)
	}
	if activity.Events[0].Direction != "a_to_b" || activity.Events[0].MessageID != messageID {
		t.Fatalf("activity event = %+v", activity.Events[0])
	}
	rawActivity, _ := json.Marshal(activity)
	if strings.Contains(string(rawActivity), "你好") || strings.Contains(string(rawActivity), "payload_bytes") {
		t.Fatalf("activity leaked content: %s", rawActivity)
	}

	status := &protocol.ChatReceipt{
		SessionCommand: protocol.SessionCommand{
			Joined: protocol.Joined{
				V:               1,
				Type:            "chat.receipt",
				RequestID:       newEpoch(t),
				RoundID:         h.RoundID(),
				ConnectionEpoch: fixture.bEpoch,
			},
			SessionID: sessionID,
		},
		MessageID: messageID,
		Status:    "processed",
	}
	if err := h.Submit(context.Background(), MessageCommand{Epoch: fixture.bEpoch, Message: status}); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	processPending(t, h)
	confirmed := lastChatStatus(fixture.aPeer)
	if confirmed == nil || confirmed.Status != "confirmed" {
		t.Fatalf("status = %+v", confirmed)
	}

	beforeDeliver := countMessages[*protocol.ChatDeliver](fixture.bPeer)
	beforeActivity := countMessages[*protocol.ActivityBatch](fixture.bPeer)
	submitChat(t, h, fixture.aEpoch, sessionID, messageID, "0", textPtr("你好"))
	if after := countMessages[*protocol.ChatDeliver](fixture.bPeer); after != beforeDeliver {
		t.Fatalf("duplicate delivered again: %d -> %d", beforeDeliver, after)
	}
	if after := countMessages[*protocol.ActivityBatch](fixture.bPeer); after != beforeActivity {
		t.Fatalf("duplicate generated activity: %d -> %d", beforeActivity, after)
	}

	submitChat(t, h, fixture.aEpoch, sessionID, messageID, "0", textPtr("不同内容"))
	if err := findLastError(fixture.aPeer); err == nil || err.Code != protocol.CodeIDConflict {
		t.Fatalf("conflict error = %+v", err)
	}

	submitChat(t, h, fixture.aEpoch, sessionID, newEpoch(t), "0", textPtr("倒退"))
	if err := findLastError(fixture.aPeer); err == nil || err.Code != protocol.CodeInvalidMessage {
		t.Fatalf("sequence error = %+v", err)
	}

	submitChat(t, h, fixture.aEpoch, sessionID, newEpoch(t), "1", textPtr("第二条"))
	if last := lastChatStatus(fixture.aPeer); last != nil && last.Status == "failed" {
		t.Fatalf("unexpected failed status: %+v", last)
	}
}

func TestInviteValidation(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	fixture := setupPair(t, h)

	self := &protocol.InviteCreate{
		Joined:   protocol.Joined{V: 1, Type: "invite.create", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.aEpoch},
		TargetID: fixture.aID,
		Mode:     protocol.ModePlaintext,
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.aEpoch, Message: self})
	processPending(t, h)
	if err := findLastError(fixture.aPeer); err == nil || err.Code != protocol.CodeInvalidMessage {
		t.Fatalf("self invite error = %+v", err)
	}

	offline := &protocol.InviteCreate{
		Joined:   protocol.Joined{V: 1, Type: "invite.create", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.aEpoch},
		TargetID: "99999999999999999999999999999999",
		Mode:     protocol.ModePlaintext,
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.aEpoch, Message: offline})
	processPending(t, h)
	if err := findLastError(fixture.aPeer); err == nil || err.Code != protocol.CodeNotMember {
		t.Fatalf("offline invite error = %+v", err)
	}
}

func TestOnlyInviteeCanAccept(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	fixture := setupPair(t, h)
	invite := &protocol.InviteCreate{
		Joined:   protocol.Joined{V: 1, Type: "invite.create", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.aEpoch},
		TargetID: fixture.bID,
		Mode:     protocol.ModePlaintext,
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.aEpoch, Message: invite})
	processPending(t, h)
	sessionID := lastSessionID(t, fixture.aPeer)

	accept := &protocol.InviteRespond{
		SessionCommand: protocol.SessionCommand{
			Joined:    protocol.Joined{V: 1, Type: "invite.respond", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.aEpoch},
			SessionID: sessionID,
		},
		Accept: boolPtr(true),
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.aEpoch, Message: accept})
	processPending(t, h)
	if err := findLastError(fixture.aPeer); err == nil || err.Code != protocol.CodeNotMember {
		t.Fatalf("inviter accept error = %+v", err)
	}
}

func TestRejectAndCancel(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	fixture := setupPair(t, h)

	invite := &protocol.InviteCreate{
		Joined:   protocol.Joined{V: 1, Type: "invite.create", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.aEpoch},
		TargetID: fixture.bID,
		Mode:     protocol.ModePlaintext,
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.aEpoch, Message: invite})
	processPending(t, h)
	sessionID := lastSessionID(t, fixture.aPeer)

	reject := &protocol.InviteRespond{
		SessionCommand: protocol.SessionCommand{
			Joined:    protocol.Joined{V: 1, Type: "invite.respond", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.bEpoch},
			SessionID: sessionID,
		},
		Accept: boolPtr(false),
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.bEpoch, Message: reject})
	processPending(t, h)
	closed := lastSessionChangedFor(t, fixture.aPeer, sessionID)
	if closed.Session.State != string(SessionClosed) || closed.Session.ClosedReason != "rejected" {
		t.Fatalf("reject session = %+v", closed.Session)
	}

	again := &protocol.InviteCreate{
		Joined:   protocol.Joined{V: 1, Type: "invite.create", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.aEpoch},
		TargetID: fixture.bID,
		Mode:     protocol.ModePlaintext,
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.aEpoch, Message: again})
	processPending(t, h)
	newSessionID := lastSessionID(t, fixture.aPeer)
	cancel := &protocol.InviteCancel{
		SessionCommand: protocol.SessionCommand{
			Joined:    protocol.Joined{V: 1, Type: "invite.cancel", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.aEpoch},
			SessionID: newSessionID,
		},
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.aEpoch, Message: cancel})
	processPending(t, h)
	closed = lastSessionChangedFor(t, fixture.aPeer, newSessionID)
	if closed.Session.State != string(SessionClosed) || closed.Session.ClosedReason != "cancelled" {
		t.Fatalf("cancel session = %+v", closed.Session)
	}
}

func TestBidirectionalInviteCreatesOneSession(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	fixture := setupPair(t, h)

	aInvite := &protocol.InviteCreate{
		Joined:   protocol.Joined{V: 1, Type: "invite.create", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.aEpoch},
		TargetID: fixture.bID, Mode: protocol.ModePlaintext,
	}
	bInvite := &protocol.InviteCreate{
		Joined:   protocol.Joined{V: 1, Type: "invite.create", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.bEpoch},
		TargetID: fixture.aID, Mode: protocol.ModePlaintext,
	}
	if err := h.Submit(context.Background(), MessageCommand{Epoch: fixture.aEpoch, Message: aInvite}); err != nil {
		t.Fatalf("a invite: %v", err)
	}
	if err := h.Submit(context.Background(), MessageCommand{Epoch: fixture.bEpoch, Message: bInvite}); err != nil {
		t.Fatalf("b invite: %v", err)
	}
	processPending(t, h)
	snapshot, _ := h.Snapshot(context.Background())
	if len(snapshot.Sessions) != 1 {
		t.Fatalf("sessions = %d", len(snapshot.Sessions))
	}
	if snapshot.Sessions[0].State != SessionInvited {
		t.Fatalf("session state = %q", snapshot.Sessions[0].State)
	}
	if err := findLastError(fixture.aPeer); err == nil || err.Code != protocol.CodeSessionExists {
		if err := findLastError(fixture.bPeer); err == nil || err.Code != protocol.CodeSessionExists {
			t.Fatal("second invite did not return SESSION_EXISTS")
		}
	}
}

func TestSessionQuotaAndRelease(t *testing.T) {
	cfg := config.Default()
	cfg.MaxSessionsPerParticipant = 1
	h := startHubWithConfig(t, cfg, func() time.Time { return fixedTime() })
	aEpoch, aPeer, aWelcome := joinAs(t, h, "A")
	bEpoch, bPeer, bWelcome := joinAs(t, h, "B")
	cEpoch, cPeer, cWelcome := joinAs(t, h, "C")
	fixture := pairFixture{aEpoch: aEpoch, bEpoch: bEpoch, aPeer: aPeer, bPeer: bPeer, aID: aWelcome.ParticipantID, bID: bWelcome.ParticipantID}

	invite := &protocol.InviteCreate{
		Joined:   protocol.Joined{V: 1, Type: "invite.create", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: aEpoch},
		TargetID: bWelcome.ParticipantID, Mode: protocol.ModePlaintext,
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: aEpoch, Message: invite})
	processPending(t, h)
	sessionID := lastSessionID(t, aPeer)

	second := &protocol.InviteCreate{
		Joined:   protocol.Joined{V: 1, Type: "invite.create", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: aEpoch},
		TargetID: cWelcome.ParticipantID, Mode: protocol.ModePlaintext,
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: aEpoch, Message: second})
	processPending(t, h)
	if err := findLastError(aPeer); err == nil || err.Code != protocol.CodeCapacityReached {
		t.Fatalf("quota error = %+v", err)
	}
	if countMessages[*protocol.SessionChanged](cPeer) != 0 {
		t.Fatal("target received invite when quota is full")
	}

	closeMsg := &protocol.SessionClose{
		SessionCommand: protocol.SessionCommand{
			Joined:    protocol.Joined{V: 1, Type: "session.close", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: aEpoch},
			SessionID: sessionID,
		},
		Reason: "user",
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: aEpoch, Message: closeMsg})
	processPending(t, h)
	beforeErrors := countMessages[*protocol.ErrorMessage](aPeer)
	_ = h.Submit(context.Background(), MessageCommand{Epoch: aEpoch, Message: second})
	processPending(t, h)
	if after := countMessages[*protocol.ErrorMessage](aPeer); after != beforeErrors {
		t.Fatalf("quota not released, errors %d -> %d", beforeErrors, after)
	}
	if countMessages[*protocol.SessionChanged](cPeer) == 0 {
		t.Fatal("target did not receive session.changed after quota released")
	}
	_ = cEpoch
	_ = fixture
}

func TestChatRequiresActiveAndMembership(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	fixture := setupPair(t, h)
	_, _, cWelcome := joinAs(t, h, "C")
	cEpoch := newEpoch(t)
	_ = cWelcome

	invite := &protocol.InviteCreate{
		Joined:   protocol.Joined{V: 1, Type: "invite.create", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.aEpoch},
		TargetID: fixture.bID, Mode: protocol.ModePlaintext,
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.aEpoch, Message: invite})
	processPending(t, h)
	sessionID := lastSessionID(t, fixture.aPeer)
	submitChat(t, h, fixture.aEpoch, sessionID, newEpoch(t), "0", textPtr("too early"))
	if err := findLastError(fixture.aPeer); err == nil || err.Code != protocol.CodeNotActive {
		t.Fatalf("early chat error = %+v", err)
	}
	_ = cEpoch
}

func TestThirdPartyCannotSendToSession(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	fixture := setupPair(t, h)
	sessionID := createPlaintextSession(t, h, fixture)
	cEpoch, cPeer, _ := joinAs(t, h, "C")

	submitChat(t, h, cEpoch, sessionID, newEpoch(t), "0", textPtr("intrude"))
	if err := findLastError(cPeer); err == nil || err.Code != protocol.CodeNotMember {
		t.Fatalf("third-party error = %+v", err)
	}
}

func TestChatModeMismatch(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	fixture := setupPair(t, h)
	sessionID := createPlaintextSession(t, h, fixture)

	msg := &protocol.ChatSend{
		SessionCommand: protocol.SessionCommand{
			Joined:    protocol.Joined{V: 1, Type: "chat.send", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.aEpoch},
			SessionID: sessionID,
		},
		MessageID:     newEpoch(t),
		Seq:           "0",
		NonceB64:      "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		CiphertextB64: "AAAAAAAAAAAAAAAAAAAAAA==",
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.aEpoch, Message: msg})
	processPending(t, h)
	if err := findLastError(fixture.aPeer); err == nil || err.Code != protocol.CodeModeMismatch {
		t.Fatalf("mode mismatch error = %+v", err)
	}
}

func TestSessionCloseBlocksChat(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	fixture := setupPair(t, h)
	sessionID := createPlaintextSession(t, h, fixture)

	closeMsg := &protocol.SessionClose{
		SessionCommand: protocol.SessionCommand{
			Joined:    protocol.Joined{V: 1, Type: "session.close", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.aEpoch},
			SessionID: sessionID,
		},
		Reason: "user",
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.aEpoch, Message: closeMsg})
	processPending(t, h)
	closed := lastSessionChangedFor(t, fixture.bPeer, sessionID)
	if closed.Session.State != string(SessionClosed) {
		t.Fatalf("session state = %q", closed.Session.State)
	}
	submitChat(t, h, fixture.aEpoch, sessionID, newEpoch(t), "0", textPtr("after close"))
	if err := findLastError(fixture.aPeer); err == nil || err.Code != protocol.CodeNotActive {
		t.Fatalf("closed session chat error = %+v", err)
	}
}

func TestSessionCloseFailsPendingMessages(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	fixture := setupPair(t, h)
	sessionID := createPlaintextSession(t, h, fixture)
	messageID := newEpoch(t)
	submitChat(t, h, fixture.aEpoch, sessionID, messageID, "0", textPtr("pending"))

	closeMsg := &protocol.SessionClose{
		SessionCommand: protocol.SessionCommand{
			Joined:    protocol.Joined{V: 1, Type: "session.close", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.bEpoch},
			SessionID: sessionID,
		},
		Reason: "user",
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.bEpoch, Message: closeMsg})
	processPending(t, h)
	status := lastChatStatus(fixture.aPeer)
	if status == nil || status.Status != "failed" {
		t.Fatalf("status = %+v", status)
	}
}

func TestAckTimeoutAndLateConfirm(t *testing.T) {
	cfg := config.Default()
	cfg.AckTimeout = 5 * time.Second
	h := startHubWithConfig(t, cfg, func() time.Time { return fixedTime() })
	fixture := setupPair(t, h)
	sessionID := createPlaintextSession(t, h, fixture)
	messageID := newEpoch(t)
	submitChat(t, h, fixture.aEpoch, sessionID, messageID, "0", textPtr("等待确认"))

	if err := h.Submit(context.Background(), TickCommand{Now: fixedTime().Add(6 * time.Second)}); err != nil {
		t.Fatalf("tick: %v", err)
	}
	processPending(t, h)
	status := lastChatStatus(fixture.aPeer)
	if status == nil || status.Status != "unconfirmed" {
		t.Fatalf("status = %+v", status)
	}
	if count := countMessages[*protocol.ChatDeliver](fixture.bPeer); count != 1 {
		t.Fatalf("unconfirmed message was retried: deliver count = %d", count)
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
	status = lastChatStatus(fixture.aPeer)
	if status == nil || status.Status != "confirmed" {
		t.Fatalf("late confirm status = %+v", status)
	}
}

func TestOnlyRecipientCanConfirm(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	fixture := setupPair(t, h)
	sessionID := createPlaintextSession(t, h, fixture)
	messageID := newEpoch(t)
	submitChat(t, h, fixture.aEpoch, sessionID, messageID, "0", textPtr("确认归属"))

	receipt := &protocol.ChatReceipt{
		SessionCommand: protocol.SessionCommand{
			Joined:    protocol.Joined{V: 1, Type: "chat.receipt", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.aEpoch},
			SessionID: sessionID,
		},
		MessageID: messageID,
		Status:    "processed",
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.aEpoch, Message: receipt})
	processPending(t, h)
	if err := findLastError(fixture.aPeer); err == nil || err.Code != protocol.CodeNotMember {
		t.Fatalf("sender confirm error = %+v", err)
	}
}

func TestInviteExpiryAndNewSessionUnaffected(t *testing.T) {
	cfg := config.Default()
	cfg.InviteTimeout = time.Second
	now := fixedTime()
	h := startHubWithConfig(t, cfg, func() time.Time { return now })
	fixture := setupPair(t, h)

	invite := &protocol.InviteCreate{
		Joined:   protocol.Joined{V: 1, Type: "invite.create", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.aEpoch},
		TargetID: fixture.bID, Mode: protocol.ModePlaintext,
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.aEpoch, Message: invite})
	processPending(t, h)
	firstID := lastSessionID(t, fixture.aPeer)

	now = fixedTime().Add(2 * time.Second)
	_ = h.Submit(context.Background(), TickCommand{Now: now})
	processPending(t, h)
	closed := lastSessionChangedFor(t, fixture.aPeer, firstID)
	if closed.Session.State != string(SessionClosed) || closed.Session.ClosedReason != "invite_timeout" {
		t.Fatalf("expired session = %+v", closed.Session)
	}
	lateAccept := &protocol.InviteRespond{
		SessionCommand: protocol.SessionCommand{
			Joined:    protocol.Joined{V: 1, Type: "invite.respond", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.bEpoch},
			SessionID: firstID,
		},
		Accept: boolPtr(true),
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.bEpoch, Message: lateAccept})
	processPending(t, h)
	if err := findLastError(fixture.bPeer); err == nil || err.Code != protocol.CodeInviteExpired {
		t.Fatalf("late accept error = %+v", err)
	}

	newInvite := &protocol.InviteCreate{
		Joined:   protocol.Joined{V: 1, Type: "invite.create", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.aEpoch},
		TargetID: fixture.bID, Mode: protocol.ModePlaintext,
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.aEpoch, Message: newInvite})
	processPending(t, h)
	newID := lastSessionID(t, fixture.aPeer)
	if newID == firstID {
		t.Fatal("new invite reused old session id")
	}
	_ = h.Submit(context.Background(), TickCommand{Now: fixedTime().Add(1500 * time.Millisecond)})
	processPending(t, h)
	if state := lastSessionChangedFor(t, fixture.aPeer, newID).Session.State; state != string(SessionInvited) {
		t.Fatalf("old tick closed new session: %s", state)
	}
}

func startHubWithConfig(t *testing.T, cfg config.Config, now func() time.Time) *Hub {
	t.Helper()
	h, err := newHub(cfg, testLogger(), now)
	if err != nil {
		t.Fatalf("newHub: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go h.Run(ctx)
	t.Cleanup(func() {
		cancel()
		<-h.Done()
	})
	return h
}

func TestKeyTimeoutClosesNegotiatingSession(t *testing.T) {
	cfg := config.Default()
	cfg.KeyTimeout = time.Second
	now := fixedTime()
	h := startHubWithConfig(t, cfg, func() time.Time { return now })
	fixture := setupPair(t, h)

	invite := &protocol.InviteCreate{
		Joined:   protocol.Joined{V: 1, Type: "invite.create", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.aEpoch},
		TargetID: fixture.bID, Mode: protocol.ModeEncrypted,
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.aEpoch, Message: invite})
	processPending(t, h)
	sessionID := lastSessionID(t, fixture.aPeer)

	accept := &protocol.InviteRespond{
		SessionCommand: protocol.SessionCommand{
			Joined:    protocol.Joined{V: 1, Type: "invite.respond", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.bEpoch},
			SessionID: sessionID,
		},
		Accept: boolPtr(true),
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.bEpoch, Message: accept})
	processPending(t, h)
	if state := lastSessionChangedFor(t, fixture.aPeer, sessionID).Session.State; state != string(SessionNegotiating) {
		t.Fatalf("state = %s", state)
	}

	now = fixedTime().Add(2 * time.Second)
	_ = h.Submit(context.Background(), TickCommand{Now: now})
	processPending(t, h)
	closed := lastSessionChangedFor(t, fixture.aPeer, sessionID)
	if closed.Session.State != string(SessionClosed) || closed.Session.ClosedReason != "key_timeout" {
		t.Fatalf("session = %+v", closed.Session)
	}
}

func TestSlowStudentDoesNotBlockOthers(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	fixture := setupPair(t, h)
	fixture.bPeer.mu.Lock()
	fixture.bPeer.sendErr = errors.New("slow client")
	fixture.bPeer.mu.Unlock()

	event := protocol.ActivityEvent{
		SessionID: newEpoch(t),
		MessageID: newEpoch(t),
		SenderID:  fixture.aID,
		Direction: "a_to_b",
		AtUTC:     "2026-10-23T03:00:00.000Z",
	}
	if err := h.Submit(context.Background(), activityCommand{event: event}); err != nil {
		t.Fatalf("activity: %v", err)
	}
	flushActivity(t, h)

	if _, ok := findOptional[*protocol.ActivityBatch](fixture.aPeer); !ok {
		t.Fatal("healthy peer did not receive activity")
	}
	if !fixture.bPeer.isClosed() {
		t.Fatal("slow peer was not closed")
	}
}
