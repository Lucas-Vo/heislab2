// utils.go
// Purpose: Small utility helpers used by the project (byte trimming, deep copy,
// cloning helpers). Kept minimal and safe for concurrent usage.
package common

func TrimZeros(b []byte) []byte {
	i := len(b)
	for i > 0 && b[i-1] == 0 {
		i--
	}
	return b[:i]
}

func CopyElevState(st ElevState) ElevState {
	cp := st
	if st.CabRequests != nil {
		cp.CabRequests = make([]bool, len(st.CabRequests))
		copy(cp.CabRequests, st.CabRequests)
	}
	return cp
}

func DeepCopySnapshot(ns Snapshot) Snapshot {
	out := Snapshot{
		HallRequests: nil,
		States:       make(map[string]ElevState, len(ns.States)),
	}
	if ns.HallRequests != nil {
		out.HallRequests = make([][2]bool, len(ns.HallRequests))
		copy(out.HallRequests, ns.HallRequests)
	}
	for k, st := range ns.States {
		out.States[k] = CopyElevState(st)
	}
	return out
}
