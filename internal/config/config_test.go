package config

import (
	"strings"
	"testing"
	"time"
)

func TestDefaultConfigIsValid(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config must be valid: %v", err)
	}
	if cfg.TeacherAddr != "127.0.0.1:8081" {
		t.Fatalf("teacher addr = %q, want loopback default", cfg.TeacherAddr)
	}
	if cfg.MaxParticipants != 100 || cfg.MaxRawConnections != 120 || cfg.MaxIdentities != 200 {
		t.Fatalf("capacity defaults drifted: participants=%d raw=%d identities=%d",
			cfg.MaxParticipants, cfg.MaxRawConnections, cfg.MaxIdentities)
	}
	if cfg.InviteTimeout != 30*time.Second || cfg.KeyTimeout != 15*time.Second {
		t.Fatalf("timeout defaults drifted: invite=%v key=%v", cfg.InviteTimeout, cfg.KeyTimeout)
	}
	if cfg.MaxBodyBytes != 4096 || cfg.MaxCiphertextBytes != 32768 || cfg.MaxWSMessageBytes != 65536 {
		t.Fatalf("byte limits drifted: body=%d cipher=%d ws=%d",
			cfg.MaxBodyBytes, cfg.MaxCiphertextBytes, cfg.MaxWSMessageBytes)
	}
}

func TestParseOverrides(t *testing.T) {
	got, err := Parse([]string{
		"--student-addr", "0.0.0.0:9000",
		"--teacher-addr", "127.0.0.1:9001",
		"--iface-ip", "192.168.5.5",
		"--no-open",
	})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got.Config.StudentAddr != "0.0.0.0:9000" {
		t.Errorf("student addr = %q", got.Config.StudentAddr)
	}
	if got.Config.TeacherAddr != "127.0.0.1:9001" {
		t.Errorf("teacher addr = %q", got.Config.TeacherAddr)
	}
	if got.Config.IfaceIP != "192.168.5.5" {
		t.Errorf("iface ip = %q", got.Config.IfaceIP)
	}
	if !got.Config.NoOpen {
		t.Errorf("NoOpen = false, want true")
	}
}

func TestParseVersion(t *testing.T) {
	got, err := Parse([]string{"--version"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !got.ShowVersion {
		t.Fatalf("ShowVersion = false, want true")
	}
}

func TestParseRejectsUnknownFlag(t *testing.T) {
	if _, err := Parse([]string{"--not-a-flag"}); err == nil {
		t.Fatal("expected unknown flag error")
	}
}

func TestValidateRejectsNonLoopbackTeacher(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8081", "192.168.5.5:8081", "[::]:8081", ":8081"} {
		cfg := Default()
		cfg.TeacherAddr = addr
		if err := cfg.Validate(); err == nil {
			t.Errorf("teacher addr %q accepted, want rejection", addr)
		}
	}
}

func TestValidateAcceptsLoopbackTeacherForms(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8081", "127.0.0.2:8081", "[::1]:8081", "localhost:8081"} {
		cfg := Default()
		cfg.TeacherAddr = addr
		if err := cfg.Validate(); err != nil {
			t.Errorf("teacher addr %q rejected: %v", addr, err)
		}
	}
}

func TestValidateRejectsInvalidAddress(t *testing.T) {
	for _, addr := range []string{"", "127.0.0.1", "127.0.0.1:notaport", "127.0.0.1:70000", "bad host:8080"} {
		cfg := Default()
		cfg.StudentAddr = addr
		if err := cfg.Validate(); err == nil {
			t.Errorf("student addr %q accepted, want rejection", addr)
		}
	}
}

func TestValidateRejectsIfaceIPNotIPv4(t *testing.T) {
	for _, ip := range []string{"::1", "example.com", "999.1.1.1"} {
		cfg := Default()
		cfg.IfaceIP = ip
		if err := cfg.Validate(); err == nil {
			t.Errorf("iface ip %q accepted, want rejection", ip)
		}
	}
}

func TestValidateRejectsCapacityRelationViolations(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
		field  string
	}{
		{"participants above raw", func(c *Config) { c.MaxParticipants = 130 }, "max_raw_connections"},
		{"raw above identities", func(c *Config) { c.MaxRawConnections = 300 }, "max_identities"},
		{"dead timeout below heartbeat", func(c *Config) { c.DeadTimeout = time.Second }, "dead_timeout"},
		{"cipher above ws", func(c *Config) { c.MaxCiphertextBytes = 70000 }, "max_ws_message_bytes"},
		{"body above cipher", func(c *Config) { c.MaxBodyBytes = 40000 }, "max_ciphertext_bytes"},
		{"zero render interval", func(c *Config) { c.RenderInterval = 0 }, "render_interval"},
		{"zero nickname", func(c *Config) { c.MaxNicknameRunes = 0 }, "max_nickname_runes"},
		{"zero chat rate", func(c *Config) { c.ChatRatePerSec = 0 }, "chat_rate_per_sec"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("expected error mentioning %s", tc.field)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("error %q does not mention %s", err, tc.field)
			}
		})
	}
}

func TestValidateRejectsZeroPort(t *testing.T) {
	cfg := Default()
	cfg.StudentAddr = "0.0.0.0:0"
	if err := cfg.Validate(); err == nil {
		t.Fatal("port 0 accepted, want rejection")
	}
}
