package proxy

import "sync"

const bufSize = 64 * 1024

var bufPool = sync.Pool{
	New: func() any {
		b := make([]byte, bufSize)
		return &b
	},
}

func getBuf() *[]byte { return bufPool.Get().(*[]byte) }

func putBuf(b *[]byte) {
	if b == nil || len(*b) != bufSize {
		return
	}
	bufPool.Put(b)
}
