package common

import (
	"fmt"
	"net"
	"sort"
)

type Config struct {
	Port int

	// Addresses indexed by elevator id: 1..N (id 0 unused)
	AddrByID map[int]string

	// Filled by InitSelf() or by DefaultConfig()/MustDefaultConfig().
	SelfID  int
	SelfKey string
}

// DefaultConfig builds the config AND detects self.
// Safe version: returns err if self cannot be detected.
func DefaultConfig() (Config, string, error) {
	cfg := Config{
		Port: 4242,
		AddrByID: map[int]string{
			1: "10.100.23.33:4242",
			2: "10.100.23.34:4242",
			3: "10.100.23.35:4242",
		},
	}
	if err := cfg.InitSelf(); err != nil {
		return Config{}, "", err
	}
	return cfg, cfg.SelfKey, nil
}

// InitSelf detects and stores SelfID/SelfKey inside cfg.
func (c *Config) InitSelf() error {
	id, err := c.DetectSelfID()
	if err != nil {
		return err
	}
	c.SelfID = id
	c.SelfKey = fmt.Sprintf("%d", id)
	return nil
}

// ListenAddr returns an address suitable for binding a listener on all interfaces.
func (c Config) ListenAddr() string {
	return fmt.Sprintf(":%d", c.Port)
}

// DetectSelfID tries to determine which elevator this process is by matching
// configured IPs against the machine's local interface IPs.
func (c Config) DetectSelfID() (int, error) {
	localIPs, err := localInterfaceIPs()
	if err != nil {
		return 0, err
	}

	ids := make([]int, 0, len(c.AddrByID))
	for id := range c.AddrByID {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	matches := make([]int, 0, 1)
	for _, id := range ids {
		hostIP, err := hostIPFromAddr(c.AddrByID[id])
		if err != nil {
			return 0, fmt.Errorf("bad address for id %d (%q): %w", id, c.AddrByID[id], err)
		}
		if localIPs[hostIP.String()] {
			matches = append(matches, id)
		}
	}

	if len(matches) == 0 {
		return 0, fmt.Errorf("could not detect self: none of the configured IPs match local interfaces")
	}
	if len(matches) > 1 {
		return 0, fmt.Errorf("could not detect self uniquely: multiple configured IPs match local interfaces: %v", matches)
	}
	return matches[0], nil
}

// PeerAddrs returns all peers excluding self.
// Uses stored SelfID if present; otherwise detects.
func (c Config) PeerAddrs() (map[int]string, int, error) {
	selfID := c.SelfID
	if selfID == 0 {
		var err error
		selfID, err = c.DetectSelfID()
		if err != nil {
			return nil, 0, err
		}
	}

	peers := make(map[int]string, len(c.AddrByID)-1)
	for id, addr := range c.AddrByID {
		if id != selfID {
			peers[id] = addr
		}
	}
	return peers, selfID, nil
}

func hostIPFromAddr(addr string) (net.IP, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, fmt.Errorf("host is not an IP: %q", host)
	}
	if v4 := ip.To4(); v4 != nil {
		return v4, nil
	}
	return ip, nil
}

func localInterfaceIPs() (map[string]bool, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	ips := make(map[string]bool)
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			default:
				continue
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if v4 := ip.To4(); v4 != nil {
				ips[v4.String()] = true
			}
		}
	}
	return ips, nil
}

func (c Config) ExpectedKeys() []string {
	ids := make([]int, 0, len(c.AddrByID))
	for id := range c.AddrByID {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, fmt.Sprintf("%d", id))
	}
	return out
}
