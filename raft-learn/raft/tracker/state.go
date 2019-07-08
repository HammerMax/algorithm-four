package tracker

// follower的状态
type StateType uint64

const (
	// Leader节点一次不能向目标节点发送多条消息，只能待一条消息被响应之后，才能发送下一条消息。
	// 当刚刚复制完快照数据、上次MsgApp消息被拒绝（或发送失败）或Leader节点初始化时，都会导致
	// 目标节点的Progress切换到这个状态
	//
	// StateProbe indicates a follower whose last index isn't known. Such a
	// follower is "probed" (i.e. an append sent periodically) to narrow down
	// its last index. In the ideal (and common) case, only one round of probing
	// is necessary as the follower will react with a hint. Followers that are
	// probed over extended periods of time are often offline.
	StateProbe StateType = iota

	// 正常的Entry记录复制状态，Leader节点向目标节点发送完消息之后，无须等待响应，即可开始后续消息的发送
	//
	// StateReplicate is the state steady in which a follower eagerly receives
	// log entries to append to its log.
	StateReplicate

	// Leader节点正在向目标节点发送快照数据
	//
	// StateSnapshot indicates a follower that needs log entries not available
	// from the leader's Raft log. Such a follower needs a full snapshot to
	// return to StateReplicate.
	StateSnapshot
)