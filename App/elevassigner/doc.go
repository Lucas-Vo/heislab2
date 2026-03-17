// Package elevassigner contains helpers for invoking the external hall request
// assigner shipped with the course resources.
//
// The package does not implement the cost function itself. Instead it prepares
// the shared snapshot so the external assigner only sees peers that are still
// considered alive.
package elevassigner
