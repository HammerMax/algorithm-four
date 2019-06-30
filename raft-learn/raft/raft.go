package raft

import "math"

const noLimit = math.MaxUint64

type raft struct {
	// 当前节点在集群中的ID
	id uint64

	// 任期
	Term uint64

	// 当前任期中，选票投给的节点
	Vote uint64

	raftLog *raftLog

	// 已经发送出去但未收到响应的消息个数上限
	//maxInflight int

	maxMsgSize uint64

	// Leader节点会记录集群中其他节点的日志复制情况（NextIndex和MatchIndex）
}

type Config struct {
	Storage Storage

	Logger Logger

	MaxCommittedSizePerReady uint64
}

func (c *Config) validate() error {
	return nil
}

func newRaft(c *Config) *raft {
	if err := c.validate(); err != nil {
		panic(err.Error())
	}
	raftlog := newLogWithSize(c.Storage, c.L)
}