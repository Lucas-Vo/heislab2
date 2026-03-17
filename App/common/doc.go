// Package common contains data types and helpers shared across the controller.
//
// It defines the snapshot format exchanged between goroutines and between
// elevators, the fixed request matrix layout used throughout the project, the
// runtime network configuration, and a thin wrapper around the course elevator
// simulator I/O.
//
// The package uses fixed-size arrays for floors and button types. This keeps
// snapshots deterministic to merge, but it also means the current
// implementation is configured for four floors and three button types at
// compile time.
package common
