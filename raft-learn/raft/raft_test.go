package raft

import (
	"etcd/raft/tracker"
	"testing"
)

func TestProgressLeader(t *testing.T) {
	r := newTestRaft(1, []uint64{1,2}, 5, 1, NewMemoryStorage())
	r.becomeCandidate()
	r.becomeLeader()
	r.prs.Progress[2].BecomeReplicate()

	propMsg := Message{From: 1, To: 1, Type: MsgProp, Entries: []Entry{{Data: []byte("foo")}}}
	for i := 0; i < 5; i++ {
		if pr := r.prs.Progress[r.id]; pr.State != tracker.StateReplicate || pr.Match != uint64(i+1) || pr.Next != pr.Match + 1 {
			t.Errorf("unexpected progress %v", pr)
		}
		if err := r.Step(propMsg); err != nil {
			t.Fatalf("proposal resulted in error: %v", err)
		}
	}
}

func newTestConfig(id uint64, peers []uint64, election, heartbeat int, storage Storage) *Config {
	return &Config{
		ID: id,
		peers: peers,
		ElectionTick: election,
		HeartbeatTick: heartbeat,
		Storage: storage,
		MaxSizePerMsg: noLimit,
		MaxInflightMsgs: 256,
	}
}

func newTestRaft(id uint64, peers []uint64, election, heartbeat int, storage Storage) *raft {
	return newRaft(newTestConfig(id, peers, election, heartbeat, storage))
}

