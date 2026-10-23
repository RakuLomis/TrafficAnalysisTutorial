package protocol

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
)

func TestNewID(t *testing.T) {
	id, err := NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(id) {
		t.Fatalf("NewID() = %q", id)
	}
	other, err := NewID()
	if err != nil {
		t.Fatalf("NewID() second error = %v", err)
	}
	if id == other {
		t.Fatal("NewID() returned duplicate values")
	}
}

func TestNewToken(t *testing.T) {
	token, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken() error = %v", err)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(token) {
		t.Fatalf("NewToken() = %q", token)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("token not base64url: %v", err)
	}
	if len(decoded) != 32 {
		t.Fatalf("token decoded length = %d, want 32", len(decoded))
	}
	other, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken() second error = %v", err)
	}
	if token == other {
		t.Fatal("NewToken() returned duplicate values")
	}
}

func TestValidID(t *testing.T) {
	if !ValidID("00112233445566778899aabbccddeeff") {
		t.Fatal("valid lowercase ID rejected")
	}
	for _, value := range []string{
		"",
		"00112233445566778899aabbccddeef",
		"00112233445566778899aabbccddeeff0",
		"00112233445566778899AABBCCDDEEFF",
		"00112233445566778899aabbccddeefg",
		" 0112233445566778899aabbccddeeff",
	} {
		if ValidID(value) {
			t.Errorf("ValidID(%q) = true, want false", value)
		}
	}
}

func TestValidToken(t *testing.T) {
	token := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))
	if !ValidToken(token) {
		t.Fatalf("ValidToken(%q) = false", token)
	}
	for _, value := range []string{
		"",
		"short",
		token + "A",
		strings.Repeat("!", 43),
	} {
		if ValidToken(value) {
			t.Errorf("ValidToken(%q) = true, want false", value)
		}
	}
}

func TestParseSeq(t *testing.T) {
	cases := map[string]uint64{
		"0":                    0,
		"1":                    1,
		"18446744073709551615": ^uint64(0),
	}
	for text, want := range cases {
		got, err := ParseSeq(text)
		if err != nil {
			t.Errorf("ParseSeq(%q) error = %v", text, err)
			continue
		}
		if got != want {
			t.Errorf("ParseSeq(%q) = %d, want %d", text, got, want)
		}
	}

	for _, text := range []string{
		"",
		"00",
		"01",
		"-1",
		"+1",
		"18446744073709551616",
		"123456789012345678901",
		" 1",
		"1 ",
	} {
		if _, err := ParseSeq(text); err == nil {
			t.Errorf("ParseSeq(%q) accepted, want error", text)
		}
	}
}

func TestFormatSeq(t *testing.T) {
	if got := FormatSeq(0); got != "0" {
		t.Errorf("FormatSeq(0) = %q", got)
	}
	if got := FormatSeq(42); got != "42" {
		t.Errorf("FormatSeq(42) = %q", got)
	}
	if got := FormatSeq(^uint64(0)); got != "18446744073709551615" {
		t.Errorf("FormatSeq(max) = %q", got)
	}
}
