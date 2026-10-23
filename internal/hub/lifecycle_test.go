package hub

import (
	"context"
	"testing"
	"time"

	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/config"
	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/protocol"
)

func lastRoundChanged(peer *fakePeer) *protocol.RoundChanged {
	var last *protocol.RoundChanged
	for _, message := range peer.messages() {
		if changed, ok := message.(*protocol.RoundChanged); ok {
			last = changed
		}
	}
	return last
}

func lastRoundEnded(peer *fakePeer) *protocol.RoundEnded {
	var last *protocol.RoundEnded
	for _, message := range peer.messages() {
		if ended, ok := message.(*protocol.RoundEnded); ok {
			last = ended
		}
	}
	return last
}

func sendRoundControl(t *testing.T, h *Hub, teacher *fakePeer, action, requestID, expected string) {
	t.Helper()
	epoch := currentTeacherEpoch(teacher)
	var message protocol.Message
	switch action {
	case "reset":
		message = &protocol.RoundReset{V: 1, Type: "round.reset", RequestID: requestID, ExpectedRoundID: expected}
	case "end":
		message = &protocol.ClassEnd{V: 1, Type: "class.end", RequestID: requestID, ExpectedRoundID: expected}
	case "start":
		message = &protocol.ClassStart{V: 1, Type: "class.start", RequestID: requestID, ExpectedRoundID: expected}
	default:
		t.Fatalf("unknown action %s", action)
	}
	if err := h.Submit(context.Background(), MessageCommand{Epoch: epoch, Message: message}); err != nil {
		t.Fatalf("%s: %v", action, err)
	}
	processPending(t, h)
}

func TestDisconnectCascadesSessions(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	fixture := setupPair(t, h)
	sessionID := createPlaintextSession(t, h, fixture)

	if err := h.Submit(context.Background(), DisconnectCommand{Epoch: fixture.aEpoch}); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	processPending(t, h)

	closed := lastSessionChangedFor(t, fixture.bPeer, sessionID)
	if closed.Session.State != string(SessionClosed) || closed.Session.ClosedReason != "peer_offline" {
		t.Fatalf("session = %+v", closed.Session)
	}
	snapshot, _ := h.Snapshot(context.Background())
	for _, participant := range snapshot.Participants {
		if participant.ID == fixture.aID && participant.State != ParticipantOffline {
			t.Fatalf("participant state = %q", participant.State)
		}
	}
	publicSessions := h.publicSnapshot().sessions
	if len(publicSessions) != 0 {
		t.Fatalf("public sessions = %+v", publicSessions)
	}
}

func TestResumeRestoresIdentityAndClosesSessions(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	aEpoch, aPeer, aWelcome := joinAs(t, h, "A")
	bEpoch, bPeer, bWelcome := joinAs(t, h, "B")
	fixture := pairFixture{aEpoch: aEpoch, bEpoch: bEpoch, aPeer: aPeer, bPeer: bPeer, aID: aWelcome.ParticipantID, bID: bWelcome.ParticipantID}
	createPlaintextSession(t, h, fixture)

	if err := h.Submit(context.Background(), DisconnectCommand{Epoch: aEpoch}); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	processPending(t, h)

	newEpoch := newEpoch(t)
	newPeer := &fakePeer{}
	newConn := NewConnection(newEpoch, fixedTime())
	newConn.Peer = newPeer
	reply := make(chan error, 1)
	if err := h.Submit(context.Background(), ConnectCommand{Conn: newConn, Reply: reply}); err != nil {
		t.Fatalf("connect new: %v", err)
	}
	if err := <-reply; err != nil {
		t.Fatalf("connect new error = %v", err)
	}
	resume := &protocol.Resume{V: 1, Type: "resume", RequestID: newEpoch, RoundID: h.RoundID(), ResumeToken: aWelcome.ResumeToken}
	if err := h.Submit(context.Background(), MessageCommand{Epoch: newEpoch, Message: resume}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	processPending(t, h)
	welcome := findMessage[*protocol.Welcome](t, newPeer)
	if welcome.ParticipantID != aWelcome.ParticipantID || welcome.Nickname != "A" {
		t.Fatalf("welcome = %+v", welcome)
	}
	if welcome.ConnectionEpoch != newEpoch {
		t.Fatalf("epoch = %q", welcome.ConnectionEpoch)
	}
	if welcome.ResumeToken == aWelcome.ResumeToken || !protocol.ValidToken(welcome.ResumeToken) {
		t.Fatalf("resume token not rotated: %q", welcome.ResumeToken)
	}
	snapshot, _ := h.Snapshot(context.Background())
	if len(snapshot.Sessions) != 1 || snapshot.Sessions[0].State != SessionClosed {
		t.Fatalf("sessions = %+v", snapshot.Sessions)
	}
	for _, participant := range snapshot.Participants {
		if participant.ID == aWelcome.ParticipantID && participant.ConnectionEpoch != newEpoch {
			t.Fatalf("participant epoch = %q", participant.ConnectionEpoch)
		}
	}

	oldConn := newConnectionForResume(t, h)
	oldReply := make(chan error, 1)
	if err := h.Submit(context.Background(), ConnectCommand{Conn: oldConn, Reply: oldReply}); err != nil {
		t.Fatalf("connect old resume: %v", err)
	}
	<-oldReply
	oldResume := &protocol.Resume{V: 1, Type: "resume", RequestID: newEpoch, RoundID: h.RoundID(), ResumeToken: aWelcome.ResumeToken}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: oldConn.Epoch, Message: oldResume})
	processPending(t, h)
	if err := findLastError(oldConn.Peer.(*fakePeer)); err == nil || err.Code != protocol.CodeResumeExpired {
		t.Fatalf("old token error = %+v", err)
	}
}

func newConnectionForResume(t *testing.T, h *Hub) *Connection {
	t.Helper()
	peer := &fakePeer{}
	conn := NewConnection(newEpoch(t), fixedTime())
	conn.Peer = peer
	return conn
}

func TestResumeAfterWindowExpiresIdentity(t *testing.T) {
	cfg := config.Default()
	cfg.ResumeWindow = time.Second
	now := fixedTime()
	h := startHubWithConfig(t, cfg, func() time.Time { return now })
	aEpoch, _, aWelcome := joinAs(t, h, "A")
	joinAs(t, h, "B")
	_ = h.Submit(context.Background(), DisconnectCommand{Epoch: aEpoch})
	processPending(t, h)

	now = fixedTime().Add(2 * time.Second)
	_ = h.Submit(context.Background(), TickCommand{Now: now})
	processPending(t, h)
	snapshot, _ := h.Snapshot(context.Background())
	for _, participant := range snapshot.Participants {
		if participant.ID == aWelcome.ParticipantID {
			t.Fatal("offline participant was not removed")
		}
	}
}

func TestRoundResetIdempotent(t *testing.T) {
	h, token := startHubWithAdmin(t, config.Default())
	teacher := authenticateTeacher(t, h, token)
	fixture := setupPair(t, h)
	createPlaintextSession(t, h, fixture)
	oldRound := h.RoundID()
	requestID := newEpoch(t)
	sendRoundControl(t, h, teacher, "reset", requestID, oldRound)
	changed := lastRoundChanged(teacher)
	if changed == nil || changed.Action != "reset" || changed.Round.ID == oldRound {
		t.Fatalf("round changed = %+v", changed)
	}
	if h.RoundID() != changed.Round.ID {
		t.Fatalf("hub round = %q", h.RoundID())
	}
	ended := lastRoundEnded(fixture.aPeer)
	if ended == nil || ended.Reason != "reset" || ended.NewRoundID != changed.Round.ID {
		t.Fatalf("round ended = %+v", ended)
	}
	snapshot, _ := h.Snapshot(context.Background())
	if len(snapshot.Participants) != 0 || len(snapshot.Sessions) != 0 {
		t.Fatalf("state not cleared: %+v", snapshot)
	}

	sendRoundControl(t, h, teacher, "reset", requestID, oldRound)
	again := lastRoundChanged(teacher)
	if again == nil || again.Round.ID != changed.Round.ID {
		t.Fatalf("idempotent retry = %+v", again)
	}

	sendRoundControl(t, h, teacher, "end", requestID, oldRound)
	if err := findLastError(teacher); err == nil || err.Code != protocol.CodeIDConflict {
		t.Fatalf("conflict error = %+v", err)
	}
}

func TestClassEndStartAndShutdown(t *testing.T) {
	h, token := startHubWithAdmin(t, config.Default())
	teacher := authenticateTeacher(t, h, token)
	oldRound := h.RoundID()
	endRequest := newEpoch(t)
	sendRoundControl(t, h, teacher, "end", endRequest, oldRound)
	ended := lastRoundChanged(teacher)
	if ended == nil || ended.Round.Status != "ENDED" || ended.Action != "end" {
		t.Fatalf("end = %+v", ended)
	}
	lateEpoch, latePeer := connectPeer(t, h, false)
	join := &protocol.Join{V: 1, Type: "join", RequestID: newEpoch(t), RoundID: ended.Round.ID, Nickname: "late"}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: lateEpoch, Message: join})
	processPending(t, h)
	if err := findLastError(latePeer); err == nil || err.Code != protocol.CodeClassEnded {
		t.Fatalf("join after end = %+v", err)
	}

	startRequest := newEpoch(t)
	sendRoundControl(t, h, teacher, "start", startRequest, ended.Round.ID)
	started := lastRoundChanged(teacher)
	if started == nil || started.Round.Status != "RUNNING" || started.Action != "start" {
		t.Fatalf("start = %+v", started)
	}
	studentEpoch, studentPeer := connectPeer(t, h, false)
	join = &protocol.Join{V: 1, Type: "join", RequestID: newEpoch(t), RoundID: started.Round.ID, Nickname: "student"}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: studentEpoch, Message: join})
	processPending(t, h)
	_ = findMessage[*protocol.Welcome](t, studentPeer)

	shutdownEpoch := currentTeacherEpoch(teacher)
	shutdown := &protocol.ServerShutdown{V: 1, Type: "server.shutdown", RequestID: newEpoch(t)}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: shutdownEpoch, Message: shutdown})
	select {
	case <-h.ShutdownRequests():
	case <-time.After(time.Second):
		t.Fatal("shutdown request not signalled")
	}
}
