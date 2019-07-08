package raft

var (
	emptyState = HardState{}
)

func isHardStateEqual(a, b HardState) bool {
	return a.Term == b.Term && a.Vote == b.Vote && a.Commit == b.Commit
}

// IsEmptySnap returns true if the given Snapshot is empty.
func IsEmptySnap(sp Snapshot) bool {
	return sp.Metadata.Index == 0
}