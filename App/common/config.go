package common

import (
	"Network-go/network/localip"
	"fmt"
	"sort"
)

type Config struct {
	Ports    []int
	HostByID map[int]string
	SelfID   int
	SelfKey  string
}

func DefaultConfig() (Config, error) {
	config := Config{
		Ports: []int{4242, 4243},
		HostByID: map[int]string{
			1: "10.100.23.19",
			2: "10.100.23.20",
			3: "10.100.23.22",
			//4: "192.168.0.197", // filip ip
			//5: "10.22.135.140", // veetel ip
			// 6: "10.24.64.186", // lucas ip
		},
	}
	if err := config.initSelf(); err != nil {
		return Config{}, err
	}
	return config, nil
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
	ip, err := localip.LocalIP()
	if err != nil {
		return 0, fmt.Errorf("localip lookup failed: %w", err)
	}
	for elevID, host := range c.HostByID {
		if host == ip {
			return elevID, nil
		}
	}
	return 0, fmt.Errorf("host IP %q not found in config", ip)
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
