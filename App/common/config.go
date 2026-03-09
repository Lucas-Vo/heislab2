package common

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

type Config struct {
	Ports    []int
	HostByID map[int]string
	SelfID   int
	SelfKey  string
}

func DefaultConfig() (Config, string, error) {
	config := Config{
		Ports: []int{4242, 4243},
		HostByID: map[int]string{
			1: "10.100.23.32",
			//2: "10.100.23.35",
			//3: "10.100.23.37",
			4: "192.168.0.197", // filip ip
			5: "10.22.135.140", // veetel ip
			6: "10.24.64.186",  // lucas ip
		},
	}
	if err := config.initSelf(); err != nil {
		return Config{}, "", err
	}
	return config, config.SelfKey, nil
}

func (c *Config) initSelf() error {
	elevID, err := c.detectSelfID()
	if err != nil {
		return err
	}
	c.SelfID = elevID
	c.SelfKey = fmt.Sprintf("%d", elevID)
	return nil
}

func (c Config) detectSelfID() (int, error) {
	out, _ := exec.Command("hostname", "-I").Output()

	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0, fmt.Errorf("hostname -I returned no IPs")
	}
	ip := fields[0]
	for elevID, host := range c.HostByID {
		if host == ip {
			return elevID, nil
		}
	}
	return 0, fmt.Errorf("host IP %q not found in config", ip)
}

func (c Config) PeerAddrsForPort(port int) (map[int]string, int, error) {
	selfID := c.SelfID
	if selfID == 0 {
		var err error
		selfID, err = c.detectSelfID()
		if err != nil {
			return nil, 0, err
		}
	}

	addrByID := make(map[int]string, len(c.HostByID))
	for elevID, host := range c.HostByID {
		addrByID[elevID] = fmt.Sprintf("%s:%d", host, port)
	}

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
