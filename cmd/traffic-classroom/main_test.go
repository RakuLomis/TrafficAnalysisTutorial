package main

import (
	"regexp"
	"testing"
)

func TestStudentURLFor(t *testing.T) {
	got, err := studentURLFor("192.168.5.5", 8080)
	if err != nil {
		t.Fatalf("studentURLFor() error = %v", err)
	}
	if got != "http://192.168.5.5:8080/" {
		t.Fatalf("studentURLFor() = %q", got)
	}
}

func TestStudentURLForRejectsInvalidPort(t *testing.T) {
	for _, port := range []int{0, -1, 70000} {
		if _, err := studentURLFor("192.168.5.5", port); err == nil {
			t.Errorf("port %d accepted, want error", port)
		}
	}
}

func TestPortOf(t *testing.T) {
	cases := map[string]int{
		"0.0.0.0:8080":   8080,
		"127.0.0.1:8081": 8081,
		"[::1]:9000":     9000,
		"bad":            0,
		"host:0":         0,
		"host:99999":     0,
	}
	for addr, want := range cases {
		if got := portOf(addr); got != want {
			t.Errorf("portOf(%q) = %d, want %d", addr, got, want)
		}
	}
}

func TestRandomIDIs32LowerHex(t *testing.T) {
	id, err := randomID()
	if err != nil {
		t.Fatalf("randomID() error = %v", err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(id) {
		t.Fatalf("randomID() = %q", id)
	}
	other, err := randomID()
	if err != nil {
		t.Fatalf("randomID() second error = %v", err)
	}
	if id == other {
		t.Fatal("randomID() returned duplicate values")
	}
}
