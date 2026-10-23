package hub

import (
	"strings"
	"testing"
	"time"

	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/protocol"
)

func fixedTime() time.Time {
	return time.Date(2026, 10, 23, 3, 0, 0, 0, time.UTC)
}

func TestNewRound(t *testing.T) {
	round, err := NewRound(fixedTime())
	if err != nil {
		t.Fatalf("NewRound() error = %v", err)
	}
	if !protocol.ValidID(round.ID) {
		t.Fatalf("round ID = %q", round.ID)
	}
	if round.Status != RoundRunning {
		t.Fatalf("status = %q", round.Status)
	}
	if !round.CreatedAt.Equal(fixedTime()) {
		t.Fatalf("created at = %v", round.CreatedAt)
	}
}

func TestParticipantLifecycle(t *testing.T) {
	now := fixedTime()
	participant := NewParticipant("00112233445566778899aabbccddeeff", "小明", "A1B2", "#3B82F6", now)
	if participant.State != ParticipantOnline {
		t.Fatalf("state = %q", participant.State)
	}
	if participant.Nickname != "小明" || participant.ShortID != "A1B2" || participant.Color != "#3B82F6" {
		t.Fatalf("participant = %+v", participant)
	}

	participant.MarkOffline(now.Add(time.Second))
	if participant.State != ParticipantOffline {
		t.Fatalf("state = %q", participant.State)
	}
	if !participant.OfflineSince.Equal(now.Add(time.Second)) {
		t.Fatalf("offline since = %v", participant.OfflineSince)
	}

	participant.MarkOnline("11112222333344445555666677778888")
	if participant.State != ParticipantOnline {
		t.Fatalf("state = %q", participant.State)
	}
	if !participant.OfflineSince.IsZero() {
		t.Fatalf("offline since = %v, want zero", participant.OfflineSince)
	}

	participant.MarkRemoved()
	if participant.State != ParticipantRemoved {
		t.Fatalf("state = %q", participant.State)
	}
}

func TestNewSession(t *testing.T) {
	a := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	b := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	id := "cccccccccccccccccccccccccccccccc"
	session, err := NewSession(id, a, b, protocol.ModeEncrypted, fixedTime(), fixedTime().Add(15*time.Second))
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if session.State != SessionInvited {
		t.Fatalf("initial state = %q, want INVITED", session.State)
	}
	if !session.Contains(a) || !session.Contains(b) {
		t.Fatal("session does not contain both participants")
	}
	if session.Contains("dddddddddddddddddddddddddddddddd") {
		t.Fatal("session contains unrelated participant")
	}
	if got := session.Counterpart(a); got != b {
		t.Fatalf("Counterpart(a) = %q", got)
	}
	if got := session.Direction(a); got != "a_to_b" {
		t.Fatalf("Direction(a) = %q", got)
	}
	if got := session.Direction(b); got != "b_to_a" {
		t.Fatalf("Direction(b) = %q", got)
	}
}

func TestSessionAccept(t *testing.T) {
	plain, err := NewSession("cccccccccccccccccccccccccccccccc", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", protocol.ModePlaintext, fixedTime(), fixedTime().Add(30*time.Second))
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	plain.Accept(time.Time{})
	if plain.State != SessionActive {
		t.Fatalf("plaintext state = %q, want ACTIVE", plain.State)
	}
	if !plain.ExpiresAt.IsZero() {
		t.Fatalf("plaintext expires at = %v, want zero", plain.ExpiresAt)
	}

	encrypted, err := NewSession("dddddddddddddddddddddddddddddddd", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", protocol.ModeEncrypted, fixedTime(), fixedTime().Add(30*time.Second))
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	encrypted.Accept(fixedTime().Add(15 * time.Second))
	if encrypted.State != SessionNegotiating {
		t.Fatalf("encrypted state = %q, want NEGOTIATING", encrypted.State)
	}
	if !encrypted.ExpiresAt.Equal(fixedTime().Add(15 * time.Second)) {
		t.Fatalf("encrypted expires at = %v", encrypted.ExpiresAt)
	}
}

func TestNewSessionRejectsInvalidInput(t *testing.T) {
	validID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cases := []struct {
		name string
		id   string
		a    string
		b    string
		mode protocol.Mode
	}{
		{"bad session id", "bad", validID, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", protocol.ModePlaintext},
		{"bad a", "cccccccccccccccccccccccccccccccc", "bad", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", protocol.ModePlaintext},
		{"bad b", "cccccccccccccccccccccccccccccccc", validID, "bad", protocol.ModePlaintext},
		{"same participants", "cccccccccccccccccccccccccccccccc", validID, validID, protocol.ModePlaintext},
		{"bad mode", "cccccccccccccccccccccccccccccccc", validID, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", protocol.Mode("both")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewSession(tc.id, tc.a, tc.b, tc.mode, fixedTime(), time.Time{}); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestPairKeyIsOrderIndependent(t *testing.T) {
	a := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	b := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if PairKey(a, b) != PairKey(b, a) {
		t.Fatal("pair key depends on order")
	}
	if PairKey(a, a) != PairKey(a, a) {
		t.Fatal("pair key unstable")
	}
}

func TestSessionCloseIsTerminal(t *testing.T) {
	session, err := NewSession("cccccccccccccccccccccccccccccccc", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", protocol.ModePlaintext, fixedTime(), time.Time{})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	session.Close("user")
	if session.State != SessionClosed {
		t.Fatalf("state = %q", session.State)
	}
	if session.ClosedReason != "user" {
		t.Fatalf("reason = %q", session.ClosedReason)
	}
	if session.IsOpen() {
		t.Fatal("closed session reports open")
	}
	session.Close("again")
	if session.ClosedReason != "user" {
		t.Fatalf("repeat close changed reason to %q", session.ClosedReason)
	}
}

func TestConnectionLifecycle(t *testing.T) {
	conn := NewConnection("11112222333344445555666677778888", fixedTime())
	if conn.State != ConnectionPending {
		t.Fatalf("state = %q", conn.State)
	}
	conn.Bind("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if conn.State != ConnectionStudent {
		t.Fatalf("state = %q", conn.State)
	}
	if conn.ParticipantID != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("participant = %q", conn.ParticipantID)
	}
	conn.Close()
	if conn.State != ConnectionClosed {
		t.Fatalf("state = %q", conn.State)
	}
	if conn.IsOpen() {
		t.Fatal("closed connection reports open")
	}
}

func TestShortIDFor(t *testing.T) {
	got := ShortIDFor("abcdef0123456789abcdef0123456789")
	if got != "ABCD" {
		t.Fatalf("ShortIDFor() = %q", got)
	}
	if len(got) != 4 || strings.ToUpper(got) != got {
		t.Fatalf("ShortIDFor() = %q", got)
	}
}

func TestColorPalette(t *testing.T) {
	if len(ColorPalette) < 4 {
		t.Fatalf("palette too small: %d", len(ColorPalette))
	}
	for i := range ColorPalette {
		color := ColorForIndex(i)
		if len(color) != 7 || color[0] != '#' {
			t.Errorf("ColorForIndex(%d) = %q", i, color)
		}
	}
	if ColorForIndex(0) != ColorForIndex(len(ColorPalette)) {
		t.Fatal("palette does not wrap")
	}
}
