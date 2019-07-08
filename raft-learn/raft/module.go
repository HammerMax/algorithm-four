package raft

type MessageType int32

const (
	// Follower节点的选举计时器超时,会创建MsgHup并调用自身step
	MsgHup            MessageType = 0
	MsgBeat           MessageType = 1
	MsgProp           MessageType = 2
	MsgApp            MessageType = 3
	MsgAppResp        MessageType = 4
	MsgVote           MessageType = 5
	MsgVoteResp       MessageType = 6
	MsgSnap           MessageType = 7
	MsgHeartbeat      MessageType = 8
	MsgHeartbeatResp  MessageType = 9
	MsgUnreachable    MessageType = 10
	MsgSnapStatus     MessageType = 11
	MsgCheckQuorum    MessageType = 12
	MsgTransferLeader MessageType = 13
	MsgTimeoutNow     MessageType = 14
	MsgReadIndex      MessageType = 15
	MsgReadIndexResp  MessageType = 16
	MsgPreVote        MessageType = 17
	MsgPreVoteResp    MessageType = 18
)

type Message struct {
	Term             uint64
	Type MessageType
	To uint64
	From uint64
	LogTerm uint64
	Index uint64
	Entries []Entry
	Commit uint64
	Snapshot Snapshot

	Reject bool
	// 拒绝后告诉leader，自身最新的EntryIndex
	RejectHint       uint64
	// 竞选信息 即campaignPreElection、campaignElection、campaignTransfer
	Context []byte
}

// 自身节点一些必要信息
type HardState struct {
	Term   uint64
	Vote   uint64
	Commit uint64
}

// 当前集群中所有节点的ID
type ConfState struct {
	Nodes    []uint64
	Learners []uint64
}

// 日志
type Entry struct {
	Term  uint64
	Index uint64
	Type  EntryType
	Data  []byte
}

func (e *Entry) Size() int {
	return 1
}

// 日志类型
type EntryType int32

const (
	EntryNormal     EntryType = 0
	EntryConfChange EntryType = 1
)

// 快照
type Snapshot struct {
	Data     []byte
	Metadata SnapshotMetadata
}