package main

import (
	"strings"
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

func TestTeacherPageURL(t *testing.T) {
	got := teacherPageURL("http://127.0.0.1:8081/", "abc")
	if got != "http://127.0.0.1:8081/#token=abc" {
		t.Fatalf("teacherPageURL() = %q", got)
	}
	if !strings.Contains(teacherPageURL("http://127.0.0.1:8081", "abc"), "#token=abc") {
		t.Fatal("missing token fragment")
	}
}

func TestConsoleOpenCommand(t *testing.T) {
	opened := ""
	stopped := false
	keep := handleConsoleCommand("o", "http://127.0.0.1:8081/#token=x", func(url string) error {
		opened = url
		return nil
	}, func() { stopped = true })
	if !keep || opened != "http://127.0.0.1:8081/#token=x" || stopped {
		t.Fatalf("open command: keep=%v opened=%q stopped=%v", keep, opened, stopped)
	}
}

func TestConsoleQuitCommand(t *testing.T) {
	stopped := false
	keep := handleConsoleCommand("q", "http://127.0.0.1:8081/#token=x", func(string) error { return nil }, func() {
		stopped = true
	})
	if keep || !stopped {
		t.Fatalf("quit command: keep=%v stopped=%v", keep, stopped)
	}
}

func TestConsoleHelpAndUnknownKeepRunning(t *testing.T) {
	for _, line := range []string{"h", "unknown"} {
		if !handleConsoleCommand(line, "http://127.0.0.1:8081/", func(string) error { return nil }, func() {}) {
			t.Fatalf("command %q stopped the console", line)
		}
	}
}
