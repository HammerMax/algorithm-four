package raft

type readOnly struct {
	option ReadOnlyOption
}

func newReadOnly(option ReadOnlyOption) *readOnly {
	return &readOnly{

	}
}