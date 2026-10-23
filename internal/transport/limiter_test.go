package transport

import (
	"testing"
	"time"
)

func TestTokenBucketBurstAndRefill(t *testing.T) {
	now := time.Date(2026, 10, 23, 3, 0, 0, 0, time.UTC)
	bucket := newTokenBucket(2, 2, now)
	if !bucket.allow(now) || !bucket.allow(now) {
		t.Fatal("initial burst rejected")
	}
	if bucket.allow(now) {
		t.Fatal("burst exceeded")
	}
	if !bucket.allow(now.Add(500 * time.Millisecond)) {
		t.Fatal("refilled token rejected")
	}
	if bucket.allow(now.Add(500 * time.Millisecond)) {
		t.Fatal("refill exceeded")
	}
	if !bucket.allow(now.Add(10 * time.Second)) {
		t.Fatal("capped refill rejected")
	}
	if !bucket.allow(now.Add(10 * time.Second)) {
		t.Fatal("second capped token rejected")
	}
	if bucket.allow(now.Add(10 * time.Second)) {
		t.Fatal("bucket exceeded burst cap")
	}
}
