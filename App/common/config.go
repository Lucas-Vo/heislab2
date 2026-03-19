package common

import (
	"Network-go/network/localip"
	"fmt"
	"sort"
	"time"
)

const (
	DOOR_OPEN_DURATION = 3 * time.Second
	IO_ADDRESS         = "localhost:15657"
)

type Config struct {
	PeerPort  int
	MsgPort   int
	HostByKey map[int]string
	SelfKey   string
}

// ------------ Exported Methods -------------

func DefaultConfig() (Config, error) {
	config := Config{
		PeerPort: 4242,
		MsgPort:  4243,
		HostByKey: map[int]string{
			1: "10.100.23.30",
			2: "10.100.23.31",
			3: "10.100.23.32",
		},
	}
	if err := config.initSelf(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (c Config) ExpectedKeys() []string {
	ids := make([]int, 0, len(c.HostByKey))
	for elevID := range c.HostByKey {
		ids = append(ids, elevID)
	}
	sort.Ints(ids)

	keyStrings := make([]string, 0, len(ids))
	for _, elevID := range ids {
		keyStrings = append(keyStrings, fmt.Sprintf("%d", elevID))
	}
	return keyStrings
}

// ------------ Unexported Methods -------------

func (c *Config) initSelf() error {
	elevID, err := c.detectSelfID()
	if err != nil {
		return err
	}
	c.SelfKey = fmt.Sprintf("%d", elevID)
	return nil
}

func (c Config) detectSelfID() (int, error) {
	ip, err := localip.LocalIP()
	if err != nil {
		return 0, fmt.Errorf("localip lookup failed: %w", err)
	}
	for elevID, host := range c.HostByKey {
		if host == ip {
			return elevID, nil
		}
	}
	return 0, fmt.Errorf("host IP %q not found in config", ip)
}
