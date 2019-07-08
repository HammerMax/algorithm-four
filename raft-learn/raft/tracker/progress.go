package tracker

type Progress struct {
	// Match 对应Follower节点当前已经成功复制的Entry索引值
	// Next  对应Follower节点下一个待复制Entry的索引值
	Match, Next uint64

	// Follower的状态
	State StateType


	PendingSnapshot uint64

	// 从Leader的角度看，Follower是否存活
	RecentActive bool

	// 记录了已经发送出去，但未收到响应的消息信息
	Inflights *Inflights

	IsLearner bool

	// 能不能给这个节点发送entry
	ProbeSent bool
}

func (pr *Progress) BecomeReplicate() {

}

func (pr *Progress) BecomeProbe() {}


func (pr *Progress) ResetState(state StateType) {

}

// pr更新自身的Match和Index
func (pr *Progress) MaybeUpdate(n uint64) bool {
	var updated bool
	if pr.Match < n {
		pr.Match = n
		updated = true
		pr.ProbeAcked()
	}
	if pr.Next < n+1 {
		pr.Next = n + 1
	}
	return updated
}

func (pr *Progress) ProbeAcked() {
	pr.ProbeSent = false
}

// 是否能向该Progress发送日志消息。
// 如果该节点处于StateSnapshot状态，说明节点需要快照信息。则不能发送日志消息
// 或者Infilght满了
//
// IsPaused returns whether sending log entries to this node has been throttled.
// This is done when a node has rejected recent MsgApps, is currently waiting
// for a snapshot, or has reached the MaxInflightMsgs limit. In normal
// operation, this is false. A throttled node will be contacted less frequently
// until it has reached a state in which it's able to accept a steady stream of
// log entries again.
func (pr *Progress) IsPaused() bool {
	switch pr.State {
	case StateProbe:
		return pr.ProbeSent
	case StateReplicate:
		return pr.Inflights.Full()
	case StateSnapshot:
		return true
	default:
		panic("unexpected state")
	}
}

func (pr *Progress) OptimisticUpdate(n uint64) { pr.Next = n + 1 }

// BecomeSnapshot moves the Progress to StateSnapshot with the specified pending
// snapshot index.
func (pr *Progress) BecomeSnapshot(snapshoti uint64) {
	pr.ResetState(StateSnapshot)
	pr.PendingSnapshot = snapshoti
}

// maybeDecrTo 两个参数都是MsgAppResp消息携带的信息:
// rejected 是被拒绝MsgApp消息的Index字段值,即Leader认为的pr的nextIndex
// last 是被拒绝MsgAppResp消息的RejectHint字段值
// 方法的目的: 根据给定的MsgAppResp消息，将Progress的NextIndex重新设置为last
func (pr *Progress) MaybeDecrTo(rejected, last uint64) bool {
	if pr.State == StateReplicate {
		// The rejection must be stale if the progress has matched and "rejected"
		// is smaller than "match".
		if rejected <= pr.Match {  //MsgAppResp已过时
			return false
		}

		// leader每次发送MsgApp时，都乐观的认为一定成功，都调用了OptimisticUpdate。所以next会比match大很多
		// 如果follower返回reject：false，则将pr.Next重新恢复到pr.match
		pr.Next = pr.Match + 1
		return true
	}

	// 消息已过时
	//
	// The rejection must be stale if "rejected" does not match next - 1. This
	// is because non-replicating followers are probed one entry at a time.
	if pr.Next-1 != rejected {
		return false
	}

	if pr.Next = min(rejected, last+1); pr.Next < 1 {
		pr.Next = 1
	}
	// 只要Follower拒绝了正常的MsgApp，probeSent设置为false
	pr.ProbeSent = false
	return true
}

// utils里有这两个方法，但此处还要重写一边，可能是希望内部包不依赖外部包（否则Cycle import）
func max(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func min(a, b uint64) uint64 {
	if a > b {
		return b
	}
	return a
}