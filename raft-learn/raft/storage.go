package raft

import (
	"errors"
	"sync"
)

// ErrCompacted Storage.Entries/Compact方法返回，当requested index因早于snapshot而不可用
var ErrCompacted = errors.New("requested index is unavailable due to compaction")

// ErrSnapOutOfData 要应用snapshot比现有的snapshot旧
var ErrSnapOutOfDate = errors.New("requested index is older than the existing snapshot")

// ErrUnavailable 当request index对应的日志不可用
var ErrUnavailable = errors.New("requested entry at index is unavailable")

type Storage interface {
	InitialState() (HardState, ConfState, error)

	// 根据给定的lo, hi, maxSize，返回当前节点的存储的日志
	Entries(lo, hi, maxSize uint64) ([]Entry, error)

	// 查询指定index对应的Entry的Term值
	Term(i uint64) (uint64, error)

	// 最后一条日志的索引
	LastIndex() (uint64, error)

	// 第一条日志索引，该日志之前的日志均已被包含进最近一次的SnapShot
	FirstIndex() (uint64, error)

	// 返回最近一次生成的快照
	Snapshot() (Snapshot, error)
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

// 快照
type Snapshot struct {
	Data     []byte
	Metadata SnapshotMetadata
}

type SnapshotMetadata struct {
	ConfState ConfState
	Index     uint64
	Term      uint64
}

// 一个基于内存的Storage的实现
type MemoryStorage struct {
	// 返回第一条日志索引和最后一条日志索引需要锁
	sync.Mutex

	hardState HardState
	snapshot  Snapshot

	// ents[i]对应的日志索引：i+snapshot.Metadata.Index
	ents []Entry
}

func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		// 初始化时添加一个 dummy Entry
		ents: make([]Entry, 1),
	}
}

// 返回自身的hardState和快照中保存的ConfState, MemoryStorage本身不存储集群状态
func (ms *MemoryStorage) InitialState() (HardState, ConfState, error) {
	return ms.hardState, ms.snapshot.Metadata.ConfState, nil
}

func (ms *MemoryStorage) LastIndex() (uint64, error) {
	ms.Lock()
	defer ms.Unlock()
	return ms.lastIndex(), nil
}

func (ms *MemoryStorage) lastIndex() uint64 {
	return ms.ents[0].Index + uint64(len(ms.ents)) - 1
}

func (ms *MemoryStorage) FirstIndex() (uint64, error) {
	ms.Lock()
	defer ms.Unlock()
	return ms.firstIndex(), nil
}

func (ms *MemoryStorage) firstIndex() uint64 {
	return ms.ents[0].Index + 1
}

// 将传入的快照snap应用于MemoryStorage自身
func (ms *MemoryStorage) ApplySnapshot(snap Snapshot) error {
	ms.Lock()
	defer ms.Unlock()

	// 应用前做一些检验:是否比已有的snapshot新
	msIndex := ms.snapshot.Metadata.Index
	snapIndex := snap.Metadata.Index
	if msIndex >= snapIndex {
		return ErrSnapOutOfDate
	}

	ms.snapshot = snap
	// 初始化ents，并赋值ents[0]
	ms.ents = []Entry{{Term: snap.Metadata.Term, Index: snap.Metadata.Index}}
	return nil
}

func (ms *MemoryStorage) Entries(lo, hi, maxSize uint64) ([]Entry, error) {
	ms.Lock()
	defer ms.Unlock()
	// 如果 lo 早于snapshot，返回错误
	offset := ms.ents[0].Index
	if lo <= offset {
		return nil, ErrCompacted
	}
	if hi > ms.lastIndex()+1 {
		raftLogger.Panicf("entries' hi(%d) is out of bound lastindex(%d)", hi, ms.lastIndex())
	}
	// 只包含在初始化时的dummy Entry
	if len(ms.ents) == 1 {
		return nil, ErrUnavailable
	}

	ents := ms.ents[lo-offset : hi-offset]
	return limitSize(ents, maxSize), nil
}

func (ms *MemoryStorage) Term(i uint64) (uint64, error) {
	ms.Lock()
	defer ms.Unlock()
	offset := ms.ents[0].Index
	if i < offset {
		return 0, ErrCompacted
	}
	if int(i-offset) >= len(ms.ents) {
		return 0, ErrUnavailable
	}
	return ms.ents[i-offset].Term, nil
}

// 追加日志ents
func (ms *MemoryStorage) Append(entries []Entry) error {
	if len(entries) == 0 {
		return nil
	}

	ms.Lock()
	defer ms.Unlock()

	first := ms.firstIndex()
	last := entries[0].Index + uint64(len(entries)) - 1

	if last < first {
		return nil
	}

	// 删除已经在快照中的entries
	if first > entries[0].Index {
		entries = entries[first-entries[0].Index:]
	}

	offset := entries[0].Index - ms.ents[0].Index
	switch {
	case uint64(len(ms.ents)) > offset:
		// 保留MemoryStorage.ents中first~offset的部分，offset之后的全部抛弃
		ms.ents = append([]Entry{}, ms.ents[:offset]...)
		// 将entries追加在ms.ents之后
		ms.ents = append(ms.ents, entries...)
	case uint64(len(ms.ents)) == offset:
		// 正好接在ms.ents之后,直接append
		ms.ents = append(ms.ents, entries...)
	default:
		raftLogger.Panicf("missing log entry [last: %d, append at: %d]", ms.lastIndex(), entries[0].Index)
	}
	return nil
}

// i: 新建SnapShot包含的最大的索引值, cs 当前集群的状态, data SnapShot的具体数据
func (ms *MemoryStorage) CreateSnapshot(i uint64, cs *ConfState, data []byte) (Snapshot, error) {
	ms.Lock()
	defer ms.Unlock()
	if i <= ms.snapshot.Metadata.Index {
		return Snapshot{}, ErrSnapOutOfDate
	}

	offset := ms.ents[0].Index
	if i > ms.lastIndex() {
		raftLogger.Panicf("snapshot %d is out of bound lastindex(%d)", i, ms.lastIndex())
	}

	ms.snapshot.Metadata.Index = i
	ms.snapshot.Metadata.Term = ms.ents[i-offset].Term
	if cs != nil {
		ms.snapshot.Metadata.ConfState = *cs
	}
	ms.snapshot.Data = data
	return ms.snapshot, nil
}

func (ms *MemoryStorage) Compact(compactIndex uint64) error {
	ms.Lock()
	defer ms.Unlock()
	offset := ms.ents[0].Index
	if compactIndex <= offset {
		return ErrCompacted
	}
	if compactIndex > ms.lastIndex() {
		raftLogger.Panicf("compact %d is out of bound lastindex(%d)", compactIndex, ms.lastIndex())
	}

	i := compactIndex - offset
	ents := make([]Entry, 1, 1+uint64(len(ms.ents))-i)
	ents[0].Index = ms.ents[i].Index
	ents[0].Term = ms.ents[i].Term
	ents = append(ents, ms.ents[i+1:]...)
	ms.ents = ents
	return nil
}

func (ms *MemoryStorage) Snapshot() (Snapshot, error) {
	ms.Lock()
	defer ms.Unlock()
	return ms.snapshot, nil
}
