package observe

import (
	"strings"
	"testing"

	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/protocol"
)

func record(messageID, direction string, mono int64, payload int, status string) protocol.ObserverRecord {
	return protocol.ObserverRecord{
		SessionID:      strings.Repeat("s", 32),
		MessageID:      messageID,
		SenderID:       strings.Repeat("a", 32),
		Direction:      direction,
		Mode:           "plaintext",
		PayloadBytes:   payload,
		WSMessageBytes: payload + 100,
		AtUTC:          "2026-10-23T03:00:00.000Z",
		MonoMS:         mono,
		ReceiptStatus:  status,
	}
}

func TestObserveIntervalsDirectionsAndStats(t *testing.T) {
	store := New(200, 1<<20, 32<<20)
	first := store.Add(record("m1", "a_to_b", 1000, 10, "pending"))
	if first.IntervalMS != nil {
		t.Fatal("first interval should be nil")
	}
	second := store.Add(record("m2", "a_to_b", 2500, 20, "pending"))
	if second.IntervalMS == nil || *second.IntervalMS != 1500 {
		t.Fatalf("second interval = %v", second.IntervalMS)
	}
	third := store.Add(record("m3", "b_to_a", 3000, 30, "pending"))
	if third.IntervalMS == nil || *third.IntervalMS != 500 {
		t.Fatalf("third interval = %v", third.IntervalMS)
	}
	snapshot, ok := store.Snapshot(strings.Repeat("s", 32))
	if !ok {
		t.Fatal("snapshot missing")
	}
	if snapshot.Stats.MessageCount != 3 {
		t.Fatalf("message count = %d", snapshot.Stats.MessageCount)
	}
	if snapshot.Stats.PayloadBytesTotal != 60 || snapshot.Stats.WSMessageBytesTotal != 360 {
		t.Fatalf("stats = %+v", snapshot.Stats)
	}
	if snapshot.Stats.DirectionSwitches != 1 {
		t.Fatalf("direction switches = %d", snapshot.Stats.DirectionSwitches)
	}
	if snapshot.Stats.LastDirection != "b_to_a" {
		t.Fatalf("last direction = %q", snapshot.Stats.LastDirection)
	}
}

func TestObserveReceiptCounts(t *testing.T) {
	store := New(200, 1<<20, 32<<20)
	store.Add(record("m1", "a_to_b", 1000, 10, "pending"))
	store.Add(record("m2", "a_to_b", 2000, 20, "pending"))
	updated, changed := store.UpdateReceipt(strings.Repeat("s", 32), "m1", "unconfirmed")
	if !changed || updated.ReceiptStatus != "unconfirmed" {
		t.Fatalf("update = %+v changed=%v", updated, changed)
	}
	updated, changed = store.UpdateReceipt(strings.Repeat("s", 32), "m1", "confirmed")
	if !changed || updated.ReceiptStatus != "confirmed" {
		t.Fatalf("late update = %+v changed=%v", updated, changed)
	}
	store.UpdateReceipt(strings.Repeat("s", 32), "m2", "failed")
	snapshot, _ := store.Snapshot(strings.Repeat("s", 32))
	if snapshot.Stats.ConfirmedCount != 1 || snapshot.Stats.UnconfirmedCount != 0 {
		t.Fatalf("receipt stats = %+v", snapshot.Stats)
	}
}

func TestObserveSessionHistoryBounds(t *testing.T) {
	store := New(2, 1<<20, 32<<20)
	store.Add(record("m1", "a_to_b", 1000, 10, "pending"))
	store.Add(record("m2", "a_to_b", 2000, 20, "pending"))
	store.Add(record("m3", "b_to_a", 3000, 30, "pending"))
	snapshot, _ := store.Snapshot(strings.Repeat("s", 32))
	if len(snapshot.Messages) != 2 {
		t.Fatalf("messages = %d", len(snapshot.Messages))
	}
	if snapshot.Messages[0].MessageID != "m2" || snapshot.Messages[1].MessageID != "m3" {
		t.Fatalf("messages = %+v", snapshot.Messages)
	}
	if !snapshot.Truncated {
		t.Fatal("truncated not set")
	}
	if snapshot.Stats.MessageCount != 3 {
		t.Fatalf("stats rolled back: %+v", snapshot.Stats)
	}
}

func TestObserveSessionByteBound(t *testing.T) {
	store := New(200, 1000, 32<<20)
	store.Add(record("m1", "a_to_b", 1000, 600, "pending"))
	store.Add(record("m2", "a_to_b", 2000, 600, "pending"))
	snapshot, _ := store.Snapshot(strings.Repeat("s", 32))
	if len(snapshot.Messages) != 1 || snapshot.Messages[0].MessageID != "m2" {
		t.Fatalf("messages = %+v", snapshot.Messages)
	}
	if snapshot.Stats.PayloadBytesTotal != 1200 {
		t.Fatalf("stats = %+v", snapshot.Stats)
	}
}

func TestObserveGlobalBound(t *testing.T) {
	store := New(200, 1000, 1000)
	first := record("m1", "a_to_b", 1000, 600, "pending")
	first.SessionID = strings.Repeat("a", 32)
	second := record("m2", "a_to_b", 2000, 600, "pending")
	second.SessionID = strings.Repeat("b", 32)
	store.Add(first)
	store.Add(second)
	snapshotA, _ := store.Snapshot(strings.Repeat("a", 32))
	snapshotB, _ := store.Snapshot(strings.Repeat("b", 32))
	if len(snapshotA.Messages) != 0 || !snapshotA.Truncated {
		t.Fatalf("session A = %+v", snapshotA)
	}
	if len(snapshotB.Messages) != 1 {
		t.Fatalf("session B = %+v", snapshotB)
	}
	if snapshotA.Stats.MessageCount != 1 || snapshotB.Stats.MessageCount != 1 {
		t.Fatal("stats rolled back after global eviction")
	}
}

func TestObserveReset(t *testing.T) {
	store := New(200, 1<<20, 32<<20)
	store.Add(record("m1", "a_to_b", 1000, 10, "pending"))
	store.Reset()
	if _, ok := store.Snapshot(strings.Repeat("s", 32)); ok {
		t.Fatal("snapshot survived reset")
	}
}
