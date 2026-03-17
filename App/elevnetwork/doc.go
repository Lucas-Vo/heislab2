// Package elevnetwork maintains the distributed world view of the elevator
// cluster.
//
// It wraps the course UDP peer-discovery/broadcast library and merges local and
// remote snapshots into one shared view. Hall requests are merged with OR when
// they are created and with AND when they are reported serviced. The package
// also tracks peer liveness, suppresses stale hall-call resurrection caused by
// delayed packets, and gates hall-call assignment on snapshot coherence.
package elevnetwork
