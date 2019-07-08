package quorum

type JointConfig [2]MajorityConfig


func (c JointConfig) VoteResult(votes map[uint64]bool) VoteResult {
	return VotePending
}

func (c JointConfig) IDs() map[uint64]struct{} {
	return map[uint64]struct{}{}
}