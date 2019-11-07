package circ

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewBuffer(t *testing.T) {
	var size int = 16
	var block int = 4
	buf := NewBuffer(size, block)

	require.NotNil(t, buf.buf)
	require.NotNil(t, buf.rcond)
	require.NotNil(t, buf.wcond)
	require.Equal(t, size, len(buf.buf))
	require.Equal(t, size, buf.size)
	require.Equal(t, block, buf.block)
}

func TestNewBuffer0Size(t *testing.T) {
	buf := NewBuffer(0, 0)
	require.NotNil(t, buf.buf)
	require.Equal(t, DefaultBufferSize, buf.size)
	require.Equal(t, DefaultBlockSize, buf.block)
}

func TestNewBufferUndersize(t *testing.T) {
	buf := NewBuffer(DefaultBlockSize+10, DefaultBlockSize)
	require.NotNil(t, buf.buf)
	require.Equal(t, DefaultBlockSize*2, buf.size)
	require.Equal(t, DefaultBlockSize, buf.block)
}

func TestGetPos(t *testing.T) {
	buf := NewBuffer(16, 4)
	tail, head := buf.GetPos()
	require.Equal(t, int64(0), tail)
	require.Equal(t, int64(0), head)

	buf.tail = 3
	buf.head = 11

	tail, head = buf.GetPos()
	require.Equal(t, int64(3), tail)
	require.Equal(t, int64(11), head)
}

func TestGet(t *testing.T) {
	buf := NewBuffer(16, 4)
	require.Equal(t, make([]byte, 16), buf.Get())

	buf.buf[0] = 1
	buf.buf[15] = 1
	require.Equal(t, []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}, buf.Get())
}

func TestSetPos(t *testing.T) {
	buf := NewBuffer(16, 4)
	require.Equal(t, int64(0), buf.tail)
	require.Equal(t, int64(0), buf.head)

	buf.SetPos(4, 8)
	require.Equal(t, int64(4), buf.tail)
	require.Equal(t, int64(8), buf.head)
}

func TestSet(t *testing.T) {
	buf := NewBuffer(16, 4)
	err := buf.Set([]byte{1, 1, 1, 1}, 17, 19)
	require.Error(t, err)

	err = buf.Set([]byte{1, 1, 1, 1}, 4, 8)
	require.NoError(t, err)
	require.Equal(t, []byte{0, 0, 0, 0, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0}, buf.buf)
}

func TestIndex(t *testing.T) {
	buf := NewBuffer(1024, 4)
	require.Equal(t, 512, buf.Index(512))
	require.Equal(t, 0, buf.Index(1024))
	require.Equal(t, 6, buf.Index(1030))
	require.Equal(t, 6, buf.Index(61446))
}

func TestAwaitFilled(t *testing.T) {
	tests := []struct {
		tail  int64
		head  int64
		n     int
		await int
		desc  string
	}{
		{tail: 0, head: 4, n: 4, await: 0, desc: "OK 0, 4"},
		{tail: 8, head: 11, n: 4, await: 1, desc: "OK 8, 11"},
		{tail: 102, head: 103, n: 4, await: 3, desc: "OK 102, 103"},
	}

	for i, tt := range tests {
		buf := NewBuffer(16, 4)
		buf.SetPos(tt.tail, tt.head)
		o := make(chan error)
		go func() {
			o <- buf.awaitFilled(4)
		}()

		time.Sleep(time.Millisecond)
		for j := 0; j < tt.await; j++ {
			atomic.AddInt64(&buf.head, 1)
			buf.wcond.L.Lock()
			buf.wcond.Broadcast()
			buf.wcond.L.Unlock()
		}

		require.NoError(t, <-o, "Unexpected Error [i:%d] %s", i, tt.desc)
	}
}

func TestAwaitFilledEnded(t *testing.T) {
	buf := NewBuffer(16, 4)
	o := make(chan error)
	go func() {
		o <- buf.awaitFilled(4)
	}()
	time.Sleep(time.Millisecond)
	atomic.StoreInt64(&buf.done, 1)
	buf.wcond.L.Lock()
	buf.wcond.Broadcast()
	buf.wcond.L.Unlock()

	require.Error(t, <-o)
}

func TestAwaitCapacity(t *testing.T) {
	tests := []struct {
		tail  int64
		head  int64
		n     int
		await int
		desc  string
	}{
		{tail: 0, head: 0, n: 4, await: 0, desc: "OK 0, 0"},
		{tail: 0, head: 5, n: 4, await: 0, desc: "OK 0, 5"},
		{tail: 0, head: 3, n: 6, await: 0, desc: "OK 0, 3"},
		{tail: 0, head: 10, n: 6, await: 2, desc: "OK 0, 0"},
		{tail: 2, head: 14, n: 4, await: 2, desc: "OK 2, 14"},
	}

	for i, tt := range tests {
		buf := NewBuffer(16, 4)
		buf.SetPos(tt.tail, tt.head)
		o := make(chan error)
		go func() {
			o <- buf.awaitCapacity(4)
		}()

		time.Sleep(time.Millisecond)
		for j := 0; j < tt.await; j++ {
			atomic.AddInt64(&buf.tail, 1)
			buf.rcond.L.Lock()
			buf.rcond.Broadcast()
			buf.rcond.L.Unlock()
		}

		require.NoError(t, <-o, "Unexpected Error [i:%d] %s", i, tt.desc)
	}
}

func TestAwaitCapacityEnded(t *testing.T) {
	buf := NewBuffer(16, 4)
	buf.SetPos(10, 8)
	o := make(chan error)
	go func() {
		o <- buf.awaitCapacity(4)
	}()
	time.Sleep(time.Millisecond)
	atomic.StoreInt64(&buf.done, 1)
	buf.rcond.L.Lock()
	buf.rcond.Broadcast()
	buf.rcond.L.Unlock()

	require.Error(t, <-o)
}

func TestCommitTail(t *testing.T) {
	tests := []struct {
		tail  int64
		head  int64
		n     int
		next  int64
		await int
		desc  string
	}{
		{tail: 0, head: 5, n: 4, next: 4, await: 0, desc: "OK 0, 4"},
		{tail: 0, head: 5, n: 6, next: 6, await: 1, desc: "OK 0, 5"},
	}

	for i, tt := range tests {
		buf := NewBuffer(16, 4)
		buf.SetPos(tt.tail, tt.head)
		o := make(chan error)
		go func() {
			o <- buf.CommitTail(tt.n)
		}()

		time.Sleep(time.Millisecond)
		for j := 0; j < tt.await; j++ {
			atomic.AddInt64(&buf.head, 1)
			buf.wcond.L.Lock()
			buf.wcond.Broadcast()
			buf.wcond.L.Unlock()
		}
		require.NoError(t, <-o, "Unexpected Error [i:%d] %s", i, tt.desc)
		require.Equal(t, tt.next, buf.tail, "Next tail mismatch [i:%d] %s", i, tt.desc)
	}
}

func TestCommitTailEnded(t *testing.T) {
	buf := NewBuffer(16, 4)
	o := make(chan error)
	go func() {
		o <- buf.CommitTail(5)
	}()
	time.Sleep(time.Millisecond)
	atomic.StoreInt64(&buf.done, 1)
	buf.wcond.L.Lock()
	buf.wcond.Broadcast()
	buf.wcond.L.Unlock()

	require.Error(t, <-o)
}

func TestCapDelta(t *testing.T) {
	buf := NewBuffer(16, 4)

	require.Equal(t, 0, buf.CapDelta())

	buf.SetPos(10, 15)
	require.Equal(t, 5, buf.CapDelta())
}
