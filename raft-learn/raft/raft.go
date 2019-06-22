package raft

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
	prs
}

// 本地Log
type raftLog struct {

}