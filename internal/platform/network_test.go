package platform

import (
	"net"
	"strings"
	"testing"
)

func mustIP(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("bad test IP %q", s)
	}
	return ip
}

func TestSelectPrefersPrivateNonVirtual(t *testing.T) {
	cands := []Candidate{
		{Name: "Loopback", IP: mustIP(t, "127.0.0.1"), Loopback: true},
		{Name: "Ethernet", IP: mustIP(t, "169.254.10.20"), LinkLocal: true},
		{Name: "vEthernet (WSL)", IP: mustIP(t, "172.20.0.1"), Virtual: true},
		{Name: "WLAN", IP: mustIP(t, "192.168.5.5")},
	}
	got, err := Select(cands, "")
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if !got.IP.Equal(mustIP(t, "192.168.5.5")) {
		t.Fatalf("selected %s, want WLAN 192.168.5.5", got.IP)
	}
}

func TestSelectPrefersNonVirtualPrivateOverVirtualPrivate(t *testing.T) {
	cands := []Candidate{
		{Name: "vEthernet", IP: mustIP(t, "10.0.0.1"), Virtual: true},
		{Name: "Ethernet", IP: mustIP(t, "10.0.0.2")},
	}
	got, err := Select(cands, "")
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if !got.IP.Equal(mustIP(t, "10.0.0.2")) {
		t.Fatalf("selected %s, want non-virtual 10.0.0.2", got.IP)
	}
}

func TestSelectRequestedIP(t *testing.T) {
	cands := []Candidate{
		{Name: "WLAN", IP: mustIP(t, "192.168.5.5")},
		{Name: "Ethernet", IP: mustIP(t, "10.1.2.3")},
	}
	got, err := Select(cands, "10.1.2.3")
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if got.Name != "Ethernet" {
		t.Fatalf("selected %q, want Ethernet", got.Name)
	}
}

func TestSelectRejectsUnusableRequestedIP(t *testing.T) {
	cands := []Candidate{
		{Name: "WLAN", IP: mustIP(t, "192.168.5.5")},
		{Name: "Ethernet", IP: mustIP(t, "169.254.1.1"), LinkLocal: true},
	}
	for _, requested := range []string{"169.254.1.1", "192.168.99.99", "127.0.0.1"} {
		_, err := Select(cands, requested)
		if err == nil {
			t.Fatalf("requested %q accepted, want error", requested)
		}
		if !strings.Contains(err.Error(), "192.168.5.5") {
			t.Fatalf("error should list candidates: %v", err)
		}
	}
}

func TestSelectNoUsableCandidate(t *testing.T) {
	cands := []Candidate{
		{Name: "Loopback", IP: mustIP(t, "127.0.0.1"), Loopback: true},
		{Name: "Ethernet", IP: mustIP(t, "169.254.1.1"), LinkLocal: true},
	}
	if _, err := Select(cands, ""); err == nil {
		t.Fatal("expected no usable candidate error")
	}
}

func TestIsPrivateIPv4(t *testing.T) {
	cases := map[string]bool{
		"10.0.0.1":       true,
		"172.16.0.1":     true,
		"172.31.255.254": true,
		"172.32.0.1":     false,
		"192.168.1.1":    true,
		"8.8.8.8":        false,
		"169.254.1.1":    false,
	}
	for s, want := range cases {
		if got := isPrivateIPv4(mustIP(t, s)); got != want {
			t.Errorf("isPrivateIPv4(%s) = %v, want %v", s, got, want)
		}
	}
}

func TestVirtualInterfaceName(t *testing.T) {
	for _, name := range []string{"vEthernet (WSL)", "VMware Network Adapter VMnet8", "VirtualBox Host-Only", "TAP-Windows Adapter V9", "Hyper-V Virtual Ethernet"} {
		if !looksVirtual(name) {
			t.Errorf("%q not detected as virtual", name)
		}
	}
	for _, name := range []string{"WLAN", "Ethernet", "以太网", "Wi-Fi"} {
		if looksVirtual(name) {
			t.Errorf("%q falsely detected as virtual", name)
		}
	}
}
