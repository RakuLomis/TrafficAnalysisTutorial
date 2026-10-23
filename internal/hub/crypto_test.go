package hub

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"testing"
	"time"

	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/protocol"
)

func encryptedNonce(direction byte, counter uint64, prefix byte) []byte {
	nonce := make([]byte, 24)
	nonce[0] = direction
	for i := 1; i < 16; i++ {
		nonce[i] = prefix
	}
	binary.BigEndian.PutUint64(nonce[16:24], counter)
	return nonce
}

func b64Bytes(value []byte) string {
	return base64.StdEncoding.EncodeToString(value)
}

func publicKey(fill byte) string {
	key := make([]byte, 32)
	for i := range key {
		key[i] = fill
	}
	return b64Bytes(key)
}

func createEncryptedSession(t *testing.T, h *Hub, fixture pairFixture) string {
	t.Helper()
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
	return sessionID
}

func publishKey(t *testing.T, h *Hub, epoch, sessionID, key string) {
	t.Helper()
	msg := &protocol.KeyPublish{
		SessionCommand: protocol.SessionCommand{
			Joined:    protocol.Joined{V: 1, Type: "key.publish", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: epoch},
			SessionID: sessionID,
		},
		PublicKeyB64: key,
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: epoch, Message: msg})
	processPending(t, h)
}

func sendConfirm(t *testing.T, h *Hub, epoch, sessionID string, direction byte, counter uint64) {
	t.Helper()
	msg := &protocol.KeyConfirm{
		SessionCommand: protocol.SessionCommand{
			Joined:    protocol.Joined{V: 1, Type: "key.confirm", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: epoch},
			SessionID: sessionID,
		},
		Seq:           protocol.FormatSeq(counter),
		NonceB64:      b64Bytes(encryptedNonce(direction, counter, 0x11)),
		CiphertextB64: b64Bytes(make([]byte, 16)),
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: epoch, Message: msg})
	processPending(t, h)
}

func sendReady(t *testing.T, h *Hub, epoch, sessionID, status string) {
	t.Helper()
	msg := &protocol.KeyReady{
		SessionCommand: protocol.SessionCommand{
			Joined:    protocol.Joined{V: 1, Type: "key.ready", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: epoch},
			SessionID: sessionID,
		},
		Status: status,
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: epoch, Message: msg})
	processPending(t, h)
}

func sessionState(t *testing.T, h *Hub, sessionID string) SessionState {
	t.Helper()
	snapshot, err := h.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	for _, session := range snapshot.Sessions {
		if session.ID == sessionID {
			return session.State
		}
	}
	t.Fatalf("session %s not found", sessionID)
	return ""
}

func activateEncryptedSession(t *testing.T, h *Hub, fixture pairFixture) string {
	t.Helper()
	sessionID := createEncryptedSession(t, h, fixture)
	publishKey(t, h, fixture.aEpoch, sessionID, publicKey(1))
	publishKey(t, h, fixture.bEpoch, sessionID, publicKey(2))
	sendConfirm(t, h, fixture.aEpoch, sessionID, 0, 0)
	sendConfirm(t, h, fixture.bEpoch, sessionID, 1, 0)
	sendReady(t, h, fixture.aEpoch, sessionID, "ready")
	sendReady(t, h, fixture.bEpoch, sessionID, "ready")
	if state := sessionState(t, h, sessionID); state != SessionActive {
		t.Fatalf("state = %s", state)
	}
	return sessionID
}

func TestEncryptedKeyExchangeAndChat(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	fixture := setupPair(t, h)
	sessionID := createEncryptedSession(t, h, fixture)

	publishKey(t, h, fixture.aEpoch, sessionID, publicKey(1))
	publishA := findMessage[*protocol.KeyPublishDeliver](t, fixture.bPeer)
	if publishA.SenderID != fixture.aID || publishA.PublicKeyB64 != publicKey(1) {
		t.Fatalf("publish A = %+v", publishA)
	}
	publishKey(t, h, fixture.bEpoch, sessionID, publicKey(2))
	publishB := findMessage[*protocol.KeyPublishDeliver](t, fixture.aPeer)
	if publishB.SenderID != fixture.bID || publishB.PublicKeyB64 != publicKey(2) {
		t.Fatalf("publish B = %+v", publishB)
	}

	sendConfirm(t, h, fixture.aEpoch, sessionID, 0, 0)
	confirmA := findMessage[*protocol.KeyConfirmDeliver](t, fixture.bPeer)
	if confirmA.SenderID != fixture.aID || confirmA.Seq != "0" {
		t.Fatalf("confirm A = %+v", confirmA)
	}
	sendConfirm(t, h, fixture.bEpoch, sessionID, 1, 0)
	confirmB := findMessage[*protocol.KeyConfirmDeliver](t, fixture.aPeer)
	if confirmB.SenderID != fixture.bID || confirmB.Seq != "0" {
		t.Fatalf("confirm B = %+v", confirmB)
	}

	sendReady(t, h, fixture.aEpoch, sessionID, "ready")
	if state := sessionState(t, h, sessionID); state != SessionNegotiating {
		t.Fatalf("one ready activated session: %s", state)
	}
	sendReady(t, h, fixture.bEpoch, sessionID, "ready")
	if state := sessionState(t, h, sessionID); state != SessionActive {
		t.Fatalf("state = %s", state)
	}
	active := lastSessionChangedFor(t, fixture.aPeer, sessionID)
	if active.Session.State != string(SessionActive) {
		t.Fatalf("active session = %+v", active.Session)
	}

	nonce := encryptedNonce(0, 1, 0x22)
	cipher := make([]byte, 32)
	chat := &protocol.ChatSend{
		SessionCommand: protocol.SessionCommand{
			Joined:    protocol.Joined{V: 1, Type: "chat.send", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.aEpoch},
			SessionID: sessionID,
		},
		MessageID:     newEpoch(t),
		Seq:           "1",
		NonceB64:      b64Bytes(nonce),
		CiphertextB64: b64Bytes(cipher),
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.aEpoch, Message: chat})
	processPending(t, h)
	deliver := findMessage[*protocol.ChatDeliver](t, fixture.bPeer)
	if deliver.Text != nil {
		t.Fatal("encrypted delivery contains text")
	}
	if deliver.NonceB64 != b64Bytes(nonce) || deliver.CiphertextB64 != b64Bytes(cipher) {
		t.Fatalf("deliver = %+v", deliver)
	}
	if deliver.SenderID != fixture.aID {
		t.Fatalf("sender = %q", deliver.SenderID)
	}
	if _, ok := findOptional[*protocol.ChatAccepted](fixture.aPeer); !ok {
		t.Fatal("missing chat.accepted")
	}
}

func findOptional[T any](peer *fakePeer) (T, bool) {
	for _, message := range peer.messages() {
		if typed, ok := message.(T); ok {
			return typed, true
		}
	}
	var zero T
	return zero, false
}

func TestKeyChangeClosesSession(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	fixture := setupPair(t, h)
	sessionID := createEncryptedSession(t, h, fixture)

	publishKey(t, h, fixture.aEpoch, sessionID, publicKey(1))
	publishKey(t, h, fixture.aEpoch, sessionID, publicKey(3))
	if state := sessionState(t, h, sessionID); state != SessionClosed {
		t.Fatalf("state = %s", state)
	}
	if err := findLastError(fixture.aPeer); err == nil || err.Code != protocol.CodeCryptoFailed {
		t.Fatalf("error = %+v", err)
	}
}

func TestKeyReadyFailureClosesSession(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	fixture := setupPair(t, h)
	sessionID := createEncryptedSession(t, h, fixture)
	publishKey(t, h, fixture.aEpoch, sessionID, publicKey(1))
	publishKey(t, h, fixture.bEpoch, sessionID, publicKey(2))
	sendReady(t, h, fixture.aEpoch, sessionID, "failed")
	if state := sessionState(t, h, sessionID); state != SessionClosed {
		t.Fatalf("state = %s", state)
	}
	if err := findLastError(fixture.aPeer); err == nil || err.Code != protocol.CodeCryptoFailed {
		t.Fatalf("error = %+v", err)
	}
}

func TestKeyConfirmRequiresZeroCounter(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	fixture := setupPair(t, h)
	sessionID := createEncryptedSession(t, h, fixture)
	publishKey(t, h, fixture.aEpoch, sessionID, publicKey(1))
	publishKey(t, h, fixture.bEpoch, sessionID, publicKey(2))
	sendConfirm(t, h, fixture.aEpoch, sessionID, 0, 1)
	if state := sessionState(t, h, sessionID); state != SessionClosed {
		t.Fatalf("state = %s", state)
	}
}

func TestEncryptedChatRejectsInvalidNonce(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	fixture := setupPair(t, h)
	sessionID := activateEncryptedSession(t, h, fixture)

	nonce := encryptedNonce(1, 1, 0x33)
	chat := &protocol.ChatSend{
		SessionCommand: protocol.SessionCommand{
			Joined:    protocol.Joined{V: 1, Type: "chat.send", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.aEpoch},
			SessionID: sessionID,
		},
		MessageID:     newEpoch(t),
		Seq:           "1",
		NonceB64:      b64Bytes(nonce),
		CiphertextB64: b64Bytes(make([]byte, 32)),
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.aEpoch, Message: chat})
	processPending(t, h)
	if state := sessionState(t, h, sessionID); state != SessionClosed {
		t.Fatalf("state = %s", state)
	}
	if err := findLastError(fixture.aPeer); err == nil || err.Code != protocol.CodeCryptoFailed {
		t.Fatalf("error = %+v", err)
	}
}

func TestEncryptedChatRejectsSeqMismatch(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	fixture := setupPair(t, h)
	sessionID := activateEncryptedSession(t, h, fixture)

	nonce := encryptedNonce(0, 2, 0x44)
	chat := &protocol.ChatSend{
		SessionCommand: protocol.SessionCommand{
			Joined:    protocol.Joined{V: 1, Type: "chat.send", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.aEpoch},
			SessionID: sessionID,
		},
		MessageID:     newEpoch(t),
		Seq:           "3",
		NonceB64:      b64Bytes(nonce),
		CiphertextB64: b64Bytes(make([]byte, 32)),
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.aEpoch, Message: chat})
	processPending(t, h)
	if state := sessionState(t, h, sessionID); state != SessionClosed {
		t.Fatalf("state = %s", state)
	}
}

func TestEncryptedChatRejectsPlaintextField(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	fixture := setupPair(t, h)
	sessionID := activateEncryptedSession(t, h, fixture)
	text := "plain"
	chat := &protocol.ChatSend{
		SessionCommand: protocol.SessionCommand{
			Joined:    protocol.Joined{V: 1, Type: "chat.send", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.aEpoch},
			SessionID: sessionID,
		},
		MessageID: newEpoch(t),
		Seq:       "1",
		Text:      &text,
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.aEpoch, Message: chat})
	processPending(t, h)
	if err := findLastError(fixture.aPeer); err == nil || err.Code != protocol.CodeModeMismatch {
		t.Fatalf("error = %+v", err)
	}
}

func TestCryptoFailedReceiptClosesSession(t *testing.T) {
	h, _ := startTestHub(t, func() time.Time { return fixedTime() })
	fixture := setupPair(t, h)
	sessionID := activateEncryptedSession(t, h, fixture)

	messageID := newEpoch(t)
	chat := &protocol.ChatSend{
		SessionCommand: protocol.SessionCommand{
			Joined:    protocol.Joined{V: 1, Type: "chat.send", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.aEpoch},
			SessionID: sessionID,
		},
		MessageID:     messageID,
		Seq:           "1",
		NonceB64:      b64Bytes(encryptedNonce(0, 1, 0x55)),
		CiphertextB64: b64Bytes(make([]byte, 32)),
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.aEpoch, Message: chat})
	processPending(t, h)

	receipt := &protocol.ChatReceipt{
		SessionCommand: protocol.SessionCommand{
			Joined:    protocol.Joined{V: 1, Type: "chat.receipt", RequestID: newEpoch(t), RoundID: h.RoundID(), ConnectionEpoch: fixture.bEpoch},
			SessionID: sessionID,
		},
		MessageID: messageID,
		Status:    "crypto_failed",
	}
	_ = h.Submit(context.Background(), MessageCommand{Epoch: fixture.bEpoch, Message: receipt})
	processPending(t, h)
	if state := sessionState(t, h, sessionID); state != SessionClosed {
		t.Fatalf("state = %s", state)
	}
	if status := lastChatStatus(fixture.aPeer); status == nil || status.Status != "failed" {
		t.Fatalf("status = %+v", status)
	}
}
