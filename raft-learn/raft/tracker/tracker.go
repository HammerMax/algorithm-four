package tracker

import "etcd/raft/quorum"

type ProgressTracker struct {
	Voters   quorum.JointConfig
	// Leader保存每个节点的状态
	Progress map[uint64]*Progress

	Votes map[uint64]bool

	MaxInflight int
}

func MakeProgressTracker(maxInflight int) ProgressTracker {
	p := ProgressTracker{}
	return p
}

// 初始化成员信息，id：成员ID，match：matchIndex，next：nextIndex
func (p *ProgressTracker) InitProgress(id, match, next uint64, isLearner bool) {

}

// 当前集群中，有投票资格的节点们
func (p *ProgressTracker) VoterNodes() []uint64 {
	return []uint64{}
}

func (p *ProgressTracker) ResetVotes() {

}

// 通过Visit方法，访问ProgressTracker下所有的Progress
func (p *ProgressTracker) Visit(f func(id uint64, pr *Progress)) {
	for id, pr := range p.Progress {
		f(id, pr)
	}
}

// RecordVote records that the node with the given id voted for this Raft
// instance if v == true (and declined it otherwise).
func (p *ProgressTracker) RecordVote(id uint64, v bool) {
	_, ok := p.Votes[id]
	if !ok {
		p.Votes[id] = v
	}
}

// TallyVotes returns the number of granted and rejected Votes, and whether the
// election outcome is known.
func (p *ProgressTracker) TallyVotes() (granted int, rejected int, _ quorum.VoteResult) {
	// Make sure to populate granted/rejected correctly even if the Votes slice
	// contains members no longer part of the configuration. This doesn't really
	// matter in the way the numbers are used (they're informational), but might
	// as well get it right.
	for id, pr := range p.Progress {
		if pr.IsLearner {
			continue
		}
		v, voted := p.Votes[id]
		if !voted {
			continue
		}
		if v {
			granted++
		} else {
			rejected++
		}
	}
	result := p.Voters.VoteResult(p.Votes)
	return granted, rejected, result
}

