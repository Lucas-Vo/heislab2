package common

import (
	"Network-go/network/localip"
	"fmt"
	"sort"
)

// Config contains the static network configuration for one controller process.
type Config struct {
	// PeerPort carries peer-discovery heartbeats.
	PeerPort int
	// MsgPort carries snapshot broadcasts.
	MsgPort int
	// HostByID maps logical elevator IDs to the IP addresses used to identify
	// them on the network.
	HostByID map[int]string
	// SelfID is the logical ID resolved from the local machine IP.
	SelfID int
	// SelfKey is the string key used in distributed snapshots and assigner input.
	SelfKey string
}

// DefaultConfig returns the hard-coded lab configuration and resolves the local
// elevator identity from the machine IP address.
//
// It fails when the local IP cannot be determined or is not present in
// HostByID. The current implementation does not support selecting the elevator
// identity with a command-line flag such as --id.
func DefaultConfig() (Config, error) {
	config := Config{
		PeerPort: 4242,
		MsgPort:  4243,
		HostByID: map[int]string{
			1: "10.100.23.28",
			2: "10.100.23.30",
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

// ExpectedKeys returns the configured elevator IDs encoded in the string form
// used by Snapshot.States and Snapshot.Alive.
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
