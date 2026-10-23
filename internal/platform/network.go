package platform

import (
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"sort"
	"strings"
)

type Candidate struct {
	Name      string
	IP        net.IP
	Loopback  bool
	LinkLocal bool
	Virtual   bool
}

func (c Candidate) Usable() bool {
	return c.IP != nil && c.IP.To4() != nil && !c.Loopback && !c.LinkLocal
}

func (c Candidate) String() string {
	return fmt.Sprintf("%s (%s)", c.Name, c.IP)
}

func Candidates() ([]Candidate, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("enumerate network interfaces: %w", err)
	}
	var out []Candidate
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		virtual := looksVirtual(iface.Name)
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipNet.IP.To4()
			if ip4 == nil {
				continue
			}
			out = append(out, Candidate{
				Name:      iface.Name,
				IP:        append(net.IP(nil), ip4...),
				Loopback:  ip4.IsLoopback(),
				LinkLocal: ip4.IsLinkLocalUnicast(),
				Virtual:   virtual,
			})
		}
	}
	return out, nil
}

func Select(cands []Candidate, requested string) (Candidate, error) {
	if requested != "" {
		for _, c := range cands {
			if c.Usable() && c.IP.String() == requested {
				return c, nil
			}
		}
		return Candidate{}, fmt.Errorf("iface_ip %q is not a usable local IPv4 address; candidates: %s", requested, candidateList(cands))
	}

	usable := make([]Candidate, 0, len(cands))
	for _, c := range cands {
		if c.Usable() {
			usable = append(usable, c)
		}
	}
	if len(usable) == 0 {
		return Candidate{}, fmt.Errorf("no usable LAN IPv4 address found; pass --iface-ip with one of: %s", candidateList(cands))
	}
	sort.SliceStable(usable, func(i, j int) bool {
		return candidateScore(usable[i]) < candidateScore(usable[j])
	})
	return usable[0], nil
}

func candidateScore(c Candidate) int {
	private := isPrivateIPv4(c.IP)
	switch {
	case private && !c.Virtual:
		return 0
	case private && c.Virtual:
		return 1
	case !c.Virtual:
		return 2
	default:
		return 3
	}
}

func candidateList(cands []Candidate) string {
	parts := make([]string, 0, len(cands))
	for _, c := range cands {
		state := ""
		if c.Loopback {
			state += ",loopback"
		}
		if c.LinkLocal {
			state += ",link-local"
		}
		if c.Virtual {
			state += ",virtual"
		}
		parts = append(parts, fmt.Sprintf("%s (%s%s)", c.IP, c.Name, state))
	}
	if len(parts) == 0 {
		return "(none)"
	}
	return strings.Join(parts, "; ")
}

func isPrivateIPv4(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	switch {
	case ip4[0] == 10:
		return true
	case ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31:
		return true
	case ip4[0] == 192 && ip4[1] == 168:
		return true
	default:
		return false
	}
}

func looksVirtual(name string) bool {
	n := strings.ToLower(name)
	for _, needle := range []string{
		"vethernet", "vmware", "virtualbox", "vbox", "hyper-v",
		"tap-", "tun", "npcap", "loopback", "docker", "wsl",
		"zerotier", "tailscale",
	} {
		if strings.Contains(n, needle) {
			return true
		}
	}
	return false
}

func OpenBrowser(url string) error {
	if runtime.GOOS == "windows" {
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	}
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	return exec.Command(opener, url).Start()
}
