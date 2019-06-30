package raft

// 如果找不到数据，就返回(0, false)
type unstable struct {
	snapshot *Snapshot

	entries []Entry
	offset  uint64

	logger Logger
}

func (u *unstable) maybeFirstIndex() (uint64, bool) {
	if u.snapshot != nil {
		return u.snapshot.Metadata.Index + 1, true
	}
	return 0, false
}

// 顺序：entry -> snapshot -> 都没有数据
func (u *unstable) maybeLastIndex() (uint64, bool) {
	if l := len(u.entries); l != 0 {
		return u.offset + uint64(l) - 1, true
	}
	if u.snapshot != nil {
		return u.snapshot.Metadata.Index, true
	}
	return 0, false
}

func (u *unstable) maybeTerm(i uint64) (uint64, bool) {
	if i < u.offset {
		if u.snapshot == nil {
			return 0, false
		}
		if u.snapshot.Metadata.Index == i {
			return u.snapshot.Metadata.Term, true
		}
		return 0, false
	}

	last, ok := u.maybeLastIndex()
	if !ok {
		return 0, false
	}
	if i > last {
		return 0, false
	}
	return u.entries[i-u.offset].Term, true
}

// 当Entries被写入到Storage后，会调用stableTo删除已被写入的Entries
func (u *unstable) stableTo(i, t uint64) {
	gt, ok := u.maybeTerm(i)
	if !ok {
		return
	}

	if gt == t && i >= u.offset {
		u.entries = u.entries[i+1-u.offset:]
		u.offset = i + 1
		// 伴随多次追加日志的操作，底层数组会不断增大。
		u.shrinkEntriesArray()
	}
}

// 当数组底层数组的cap是len的2倍，则缩减数组
func (u *unstable) shrinkEntriesArray() {
	const lenMultiple = 2
	if len(u.entries) == 0 {
		u.entries = nil
	} else if len(u.entries)*lenMultiple < cap(u.entries) {
		newEntries := make([]Entry, len(u.entries))
		copy(newEntries, u.entries)
		u.entries = newEntries
	}
}

// 当snapshot被写入Storage后，会调用stableSnapTo（）将snapshot删除
func (u *unstable) stableSnapTo(i uint64) {
	if u.snapshot != nil && u.snapshot.Metadata.Index == i {
		u.snapshot = nil
	}
}

// 追加Entries
func (u *unstable) truncateAndAppend(ents []Entry) {
	after := ents[0].Index
	switch {
	case after == u.offset+uint64(len(u.entries)):
		u.entries = append(u.entries, ents...)
	case after <= u.offset:
		u.logger.Infof("replace the unstable entries from index %d", after)
		u.offset = after
		u.entries = ents
	default:
		u.logger.Infof("truncate the unstable entries before index %d", after)
		u.entries = append([]Entry{}, u.slice(u.offset, after)...)
		u.entries = append(u.entries, ents...)
	}
}

func (u *unstable) slice(lo uint64, hi uint64) []Entry {
	u.mustCheckOutOfBounds(lo, hi)
	return u.entries[lo-u.offset : hi-u.offset]
}

func (u *unstable) mustCheckOutOfBounds(lo, hi uint64) {
	if lo > hi {
		u.logger.Panic("invalid unstable.slice %d > %d", lo, hi)
	}
	upper := u.offset + uint64(len(u.entries))
	if lo < u.offset || hi > upper {
		u.logger.Panicf("unstable.slice[%d,%d) out of bound [%d,%d]", lo, hi, u.offset, upper)
	}
}
