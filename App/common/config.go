// common/config.go
package common

import (
	"fmt"
	"net"
	"sort"
)

type Config struct {
	// Optional: keep if you want known ports, but StartP2P will now accept any port directly.
	Ports []int

	// Hosts indexed by elevator id: 1..N (id 0 unused)
	HostByID map[int]string

	// Filled by InitSelf() or by DefaultConfig()/MustDefaultConfig().
	SelfID  int
	SelfKey string
}

// DefaultConfig builds the config AND detects self.
// Safe version: returns err if self cannot be detected.
func DefaultConfig() (Config, string, error) {
	cfg := Config{
		Ports: []int{4242, 4243},
		HostByID: map[int]string{
			1: "10.100.23.34",
			2: "10.100.23.35",
			3: "10.100.23.37",
			4: "10.22.52.133",
			// 2: "10.100.23.33",
			// 3: "10.100.23.37",
		},
	}
	if err := cfg.InitSelf(); err != nil {
		return Config{}, "", err
	}
	return cfg, cfg.SelfKey, nil
}

// InitSelf detects and stores SelfID/SelfKey inside cfg.
func (c *Config) InitSelf() error {
	elevID, err := c.DetectSelfID()
	if err != nil {
		return err
	}
	c.SelfID = elevID
	c.SelfKey = fmt.Sprintf("%d", elevID)
	return nil
}

// ListenAddrForPort returns an address suitable for binding a listener on all interfaces.
func (c Config) ListenAddrForPort(port int) string {
	return fmt.Sprintf(":%d", port)
}

// AddrByIDForPort returns full "ip:port" addrs for a given port.
func (c Config) AddrByIDForPort(port int) map[int]string {
	addrMap := make(map[int]string, len(c.HostByID))
	for elevID, host := range c.HostByID {
		addrMap[elevID] = fmt.Sprintf("%s:%d", host, port)
	}
	return addrMap
}

func (c Config) DetectSelfID() (int, error) {
	localIPs, err := localInterfaceIPs()
	if err != nil {
		return 0, err
	}

	ids := make([]int, 0, len(c.HostByID))
	for elevID := range c.HostByID {
		ids = append(ids, elevID)
	}
	sort.Ints(ids)

	matches := make([]int, 0, 1)
	for _, elevID := range ids {
		ip := net.ParseIP(c.HostByID[elevID])
		if ip == nil {
			return 0, fmt.Errorf("host for id %d is not an IP: %q", elevID, c.HostByID[elevID])
		}
		if v4 := ip.To4(); v4 != nil {
			if localIPs[v4.String()] {
				matches = append(matches, elevID)
			}
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

// PeerAddrsForPort returns all peers excluding self for a given port.
// Uses stored SelfID if present; otherwise detects.
func (c Config) PeerAddrsForPort(port int) (map[int]string, int, error) {
	selfID := c.SelfID
	if selfID == 0 {
		var err error
		selfID, err = c.DetectSelfID()
		if err != nil {
			return nil, 0, err
		}
	}

	addrByID := c.AddrByIDForPort(port)

	peers := make(map[int]string, len(addrByID)-1)
	for elevID, addr := range addrByID {
		if elevID != selfID {
			peers[elevID] = addr
		}
	}
	return peers, selfID, nil
}

func (c Config) ExpectedKeys() []string {
	ids := make([]int, 0, len(c.HostByID))
	for elevID := range c.HostByID {
		ids = append(ids, elevID)
	}
	sort.Ints(ids)

	keyStrings := make([]string, 0, len(ids))
	for _, elevID := range ids {
		keyStrings = append(keyStrings, fmt.Sprintf("%d", elevID))
	}
	return keyStrings
}

// NOTE: hostIPFromAddr is no longer needed for self-detect since HostByID is already IPs,
// but keep it if other code still uses it.
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
