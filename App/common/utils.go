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
	snapshotCopy := Snapshot{
		HallRequests: nil,
		States:       make(map[string]ElevState, len(ns.States)),
	}
	if ns.HallRequests != nil {
		snapshotCopy.HallRequests = make([][2]bool, len(ns.HallRequests))
		copy(snapshotCopy.HallRequests, ns.HallRequests)
	}
	for k, st := range ns.States {
		snapshotCopy.States[k] = CopyElevState(st)
	}
	return snapshotCopy
}

// copyHall copies hall request slices, defaulting missing values to false.
func CopyHall(dst [][2]bool, src [][2]bool) {
	if dst == nil {
		return
	}
	for i := range dst {
		if src != nil && i < len(src) {
			dst[i] = src[i]
		} else {
			dst[i] = [2]bool{false, false}
		}
	}
}

// cloneHallSlice deep-copies a hall request matrix to a fixed-size slice.
func CloneHallSlice(in [][2]bool) [][2]bool {
	copiedHall := make([][2]bool, N_FLOORS)
	CopyHall(copiedHall, in)
	return copiedHall
}

// cloneBoolSlice deep-copies a cab request slice to a fixed-size slice.
func CloneBoolSlice(in []bool) []bool {
	copiedCab := make([]bool, N_FLOORS)
	for i := range N_FLOORS {
		if in != nil && i < len(in) {
			copiedCab[i] = in[i]
		} else {
			copiedCab[i] = false
		}
	}
	return copiedCab
}
