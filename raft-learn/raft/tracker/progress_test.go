package tracker

import (
	"testing"
)

func TestProgressString(t *testing.T) {
	ins := NewInflights(1)
	ins.Add(123)
	pr := &Progress{
		Match:	1,
		Next:	2,
		State:	StateSnapshot,
		PendingSnapshot: 123,
		RecentActive: false,
		ProbeSent: true,
		IsLearner: true,
		Inflights: ins,
	}
	const exp = `StateSnapshot match=1 next=2 learner paused pendingSnap=123 inactive inflight=1[full]`
	if act := pr.String(); act != exp {
		t.Errorf("exp: %s\nact: %s", exp, act)
	}
}

func TestProgressIsPaused(t *testing.T) {
	tests := []struct{
		state StateType
		paused bool

		want bool
	}{
		{StateProbe, false, false},
		{StateProbe, true, true},
		{StateReplicate, false, false},
		{StateReplicate, true, false},
		{StateSnapshot, false, true},
		{StateSnapshot, true, true},
	}

	for i, tt := range tests {
		p := &Progress{
			State: tt.state,
			ProbeSent: tt.paused,
			Inflights: NewInflights(256),
		}
		if g := p.IsPaused(); g != tt.want {
			t.Errorf("#%d: paused= %t, want %t", i, g, tt.want)
		}
	}
}

func TestProgressResume(t *testing.T) {
	p := &Progress{
		Next: 2,
		ProbeSent: true,
	}
	p.MaybeDecrTo(1, 1)
	if p.ProbeSent {
		t.Errorf("paused= %v, want false", p.ProbeSent)
	}
	p.ProbeSent = true
	p.MaybeUpdate(2)
	if p.ProbeSent {
		t.Errorf("paused= %v, want false", p.ProbeSent)
	}
}

