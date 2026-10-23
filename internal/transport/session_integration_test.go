package transport

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/protocol"
	"github.com/coder/websocket"
)

func readUntilType(t *testing.T, conn *websocket.Conn, want string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read until %s: %v", want, err)
		}
		if typ != websocket.MessageText {
			t.Fatalf("unexpected message type %v", typ)
		}
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &head); err != nil {
			t.Fatalf("json: %v", err)
		}
		if head.Type == want {
			return data
		}
	}
}

func joinOverWS(t *testing.T, conn *websocket.Conn, roundID, nickname string) protocol.Welcome {
	t.Helper()
	writeMessage(t, conn, &protocol.Join{
		V:         1,
		Type:      "join",
		RequestID: mustID(t),
		RoundID:   roundID,
		Nickname:  nickname,
	})
	var welcome protocol.Welcome
	if err := json.Unmarshal(readUntilType(t, conn, "welcome"), &welcome); err != nil {
		t.Fatalf("welcome: %v", err)
	}
	return welcome
}

func TestPlaintextSessionOverWebSocket(t *testing.T) {
	s := testServer(t)
	server := httptest.NewServer(s.studentHandler())
	defer server.Close()
	roundID := s.info.Hub.RoundID()

	a := dialWS(t, server.URL, "/ws")
	defer a.CloseNow()
	b := dialWS(t, server.URL, "/ws")
	defer b.CloseNow()
	aWelcome := joinOverWS(t, a, roundID, "A")
	bWelcome := joinOverWS(t, b, roundID, "B")

	invite := &protocol.InviteCreate{
		Joined: protocol.Joined{
			V:               1,
			Type:            "invite.create",
			RequestID:       mustID(t),
			RoundID:         roundID,
			ConnectionEpoch: aWelcome.ConnectionEpoch,
		},
		TargetID: bWelcome.ParticipantID,
		Mode:     protocol.ModePlaintext,
	}
	writeMessage(t, a, invite)
	var changed protocol.SessionChanged
	if err := json.Unmarshal(readUntilType(t, b, "session.changed"), &changed); err != nil {
		t.Fatalf("invite changed: %v", err)
	}
	if changed.Session.State != "INVITED" {
		t.Fatalf("invite state = %q", changed.Session.State)
	}
	sessionID := changed.Session.SessionID
	if err := json.Unmarshal(readUntilType(t, a, "session.changed"), &changed); err != nil {
		t.Fatalf("inviter changed: %v", err)
	}
	if changed.Session.State != "INVITED" {
		t.Fatalf("inviter state = %q", changed.Session.State)
	}

	accept := &protocol.InviteRespond{
		SessionCommand: protocol.SessionCommand{
			Joined: protocol.Joined{
				V:               1,
				Type:            "invite.respond",
				RequestID:       mustID(t),
				RoundID:         roundID,
				ConnectionEpoch: bWelcome.ConnectionEpoch,
			},
			SessionID: sessionID,
		},
		Accept: boolPointer(true),
	}
	writeMessage(t, b, accept)
	if err := json.Unmarshal(readUntilType(t, a, "session.changed"), &changed); err != nil {
		t.Fatalf("accept changed: %v", err)
	}
	if changed.Session.State != "ACTIVE" {
		t.Fatalf("accept state = %q", changed.Session.State)
	}

	messageID := mustID(t)
	text := "你好"
	chat := &protocol.ChatSend{
		SessionCommand: protocol.SessionCommand{
			Joined: protocol.Joined{
				V:               1,
				Type:            "chat.send",
				RequestID:       mustID(t),
				RoundID:         roundID,
				ConnectionEpoch: aWelcome.ConnectionEpoch,
			},
			SessionID: sessionID,
		},
		MessageID: messageID,
		Seq:       "0",
		Text:      &text,
	}
	writeMessage(t, a, chat)

	var accepted protocol.ChatAccepted
	if err := json.Unmarshal(readUntilType(t, a, "chat.accepted"), &accepted); err != nil {
		t.Fatalf("accepted: %v", err)
	}
	if accepted.MessageID != messageID || accepted.SessionID != sessionID {
		t.Fatalf("accepted = %+v", accepted)
	}

	var deliver protocol.ChatDeliver
	if err := json.Unmarshal(readUntilType(t, b, "chat.deliver"), &deliver); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if deliver.Text == nil || *deliver.Text != text {
		t.Fatalf("deliver = %+v", deliver)
	}
	if deliver.SenderID != aWelcome.ParticipantID {
		t.Fatalf("deliver sender = %q", deliver.SenderID)
	}

	receipt := &protocol.ChatReceipt{
		SessionCommand: protocol.SessionCommand{
			Joined: protocol.Joined{
				V:               1,
				Type:            "chat.receipt",
				RequestID:       mustID(t),
				RoundID:         roundID,
				ConnectionEpoch: bWelcome.ConnectionEpoch,
			},
			SessionID: sessionID,
		},
		MessageID: messageID,
		Status:    "processed",
	}
	writeMessage(t, b, receipt)
	var status protocol.ChatStatus
	if err := json.Unmarshal(readUntilType(t, a, "chat.status"), &status); err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Status != "confirmed" || status.MessageID != messageID {
		t.Fatalf("status = %+v", status)
	}
}

func boolPointer(value bool) *bool {
	return &value
}
