package tracker

// 主要功能：记录当前已发出但未收到响应的MsgApp消息
type Inflights struct {
	// inflights.buffer 被当作一个环形数组使用，start字段中记录buffer中第一个MsgApp的下标
	// the starting index in the buffer
	start int
	// number of inflights in the buffer
	count int

	// the size of the buffer
	size int

	// 记录MsgApp消息中最后一条Entry记录的索引值
	//
	// buffer contains the index of the last entry
	// inside one message.
	buffer []uint64
}

func NewInflights(size int) *Inflights {
	return &Inflights{
		size: size,
	}
}

// Leader每次发送MsgApp消息后，会调用Add将数量增加到inflights中
func (in *Inflights) Add(inflight uint64) {
	if in.Full() {
		panic("cannot add into a Full inflights")
	}
	next := in.start + in.count
	size := in.size
	// 环形数组的体现
	if next >= size {
		next -= size
	}
	if next >= len(in.buffer) {
		in.grow()
	}
	in.buffer[next] = inflight
	in.count++
}

// Full returns true if no more messages can be sent at the moment.
func (in *Inflights) Full() bool {
	return in.count == in.size
}

func (in *Inflights) grow() {
	newSize := len(in.buffer) * 2
	if newSize == 0 {
		newSize = 1
	} else if newSize > in.size {
		newSize = in.size
	}
	newBuffer := make([]uint64, newSize)
	copy(newBuffer, in.buffer)
	in.buffer = newBuffer
}

// FreeLE frees the inflights smaller or equal to the given `to` flight.
func (in *Inflights) FreeLE(to uint64) {

}
