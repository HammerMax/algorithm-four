package tracker

type Progress struct {
	// Match 对应Follower节点当前已经成功复制的Entry索引值
	// Next  对应Follower节点下一个待复制Entry的索引值
	Match, Next uint64

	// Follower的状态
	State StateType

	// 从Leader的角度看，Follower是否存活
	RecentActive bool

	// 记录了已经发送出去，但未收到响应的消息信息
	Inflights *Inflights
}