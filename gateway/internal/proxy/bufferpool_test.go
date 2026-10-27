package proxy

import "testing"

func TestBufferPoolUsesCopyBufferSize(t *testing.T) {
	var p bufferPool
	b := p.Get()
	if len(b) != copyBufferSize {
		t.Fatalf("Get returned %d bytes, want %d", len(b), copyBufferSize)
	}
	p.Put(b)
	p.Put(make([]byte, 32*1024)) // foreign sizes must not enter the pool
	for i := 0; i < 8; i++ {
		if got := p.Get(); len(got) != copyBufferSize {
			t.Fatalf("pool handed out a %d-byte buffer, want %d", len(got), copyBufferSize)
		}
	}
}
