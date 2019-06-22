package tracker


type ProgressTracker struct {
	// Leader保存每个节点的状态
	Progress map[uint64]*Progress
}