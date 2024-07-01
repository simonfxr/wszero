package wszero_test

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	mrand "math/rand"
	"os"
	"testing"

	"github.com/simonfxr/wszero"
)

func BenchmarkWriteMessage(b *testing.B) {
	for _, size := range []int{0, 1 << 1, 1 << 3, 1 << 5, 1 << 7, 1 << 11, 1 << 15, 1 << 16, 1 << 18} {
		foreachHandshakes(b, fmt.Sprintf("%%s[msgsz=%d]", size), clientHandshakes, nil, func(b *testing.B, h handshake[wsconn, wsconn]) {
			b.ReportAllocs()
			c, s := wsHandshakePair(h.d, h.u)
			defer c.Close()
			defer s.Close()

			go func() {
				f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
				if err != nil {
					panic(err)
				}
				defer f.Close()
				_, _ = io.CopyBuffer(f, s.NetConn(), make([]byte, max(size*2, 1<<16)))
			}()

			msg := make([]byte, size)
			_, _ = io.ReadFull(rand.Reader, msg)

			if c, _ := c.(*wszero.Conn); c != nil {
				c.SetBufferPool(wszero.NewBufferPool())
			}

			b.ResetTimer()
			b.SetBytes(int64(len(msg)))
			for i := 0; i < b.N; i++ {
				c.WriteMessage(wszero.BinaryMessage, msg)
			}
			b.StopTimer()
		})
	}
}

func BenchmarkReadMessage(b *testing.B) {

	testf := func(b *testing.B, size int, doMask, randMask bool, receiver, sender wsconn) {

		sizeLen := 0
		if size > 125 {
			sizeLen = 2
		}

		mask := [4]byte{}
		maskLen := 0
		maskBit := byte(0)
		if doMask {
			maskLen = 4
			maskBit = 0x80
			if randMask {
				binary.NativeEndian.PutUint32(mask[:], mrand.Uint32())
			}
		}

		frame := make([]byte, 2+sizeLen+maskLen+size)
		frame[0] = byte(wszero.BinaryMessage) | 0x80
		frame[1] = byte(size) | maskBit
		if size > 125 {
			frame[1] = 126 | maskBit
			binary.BigEndian.PutUint16(frame[2:], uint16(size))
		}
		if doMask {
			copy(frame[2+sizeLen:], mask[:])
		}
		_, _ = io.ReadFull(rand.Reader, frame[2+sizeLen+maskLen:])

		n := max(1, 8192/len(frame))
		frames := make([]byte, n*len(frame))
		dest := frames
		for i := 0; i < n; i++ {
			dest = dest[copy(dest, frame):]
		}

		done := make(chan struct{})
		go func() {
			defer func() { close(done) }()
			i := 0
			nrem := b.N * len(frame)
			for ; i < b.N; i += n {
				bs := frames
				if i+n > b.N {
					bs = frames[:len(frame)*(b.N-i)]
				}
				nw, err := sender.NetConn().Write(bs)
				nrem -= nw
				if err != nil {
					panic(fmt.Errorf("bad write, remaining bytes: %d: %s", nrem, err))
				}
				if nw != len(bs) {
					panic(fmt.Errorf("short write, remaining bytes: %d", nrem))
				}
			}
		}()

		rbp := wszero.BufferPool(nil)
		wsc, _ := receiver.(*wszero.Conn)

		if wsc != nil {
			rbp = wszero.NewBufferPool()
			wsc.SetBufferPool(rbp)
		}

		b.ResetTimer()
		b.SetBytes(int64(len(frame)))
		for i := 0; i < b.N; i++ {
			mt, data, err := receiver.ReadMessage()
			if err != nil {
				panic(err)
			}
			if mt != wszero.BinaryMessage || len(data) != size {
				panic("bad read")
			}
			if rbp != nil {
				rbp.PutBuffer(data)
			}
		}
		b.StopTimer()
		<-done
	}

	sizes := []int{0, 1 << 1, 1 << 3, 1 << 5, 1 << 7, 1 << 15}

	b.Run("client", func(b *testing.B) {
		for _, size := range sizes {
			foreachHandshakes(b, fmt.Sprintf("%%s[msgsz=%d]", size), clientHandshakes, nil, func(b *testing.B, h handshake[wsconn, wsconn]) {
				b.ReportAllocs()
				c, s := wsHandshakePair(h.d, h.u)
				defer c.Close()
				defer s.Close()
				testf(b, size, false, false, c, s)
			})
		}
	})

	for _, rand := range []bool{false, true} {
		name := "server-zeromask"
		if rand {
			name = "server-randmask"
		}
		b.Run(name, func(b *testing.B) {
			for _, size := range sizes {
				foreachHandshakes(b, fmt.Sprintf("%%s[msgsz=%d]", size), serverHandshakes, nil, func(b *testing.B, h handshake[wsconn, wsconn]) {
					b.ReportAllocs()
					c, s := wsHandshakePair(h.d, h.u)
					defer c.Close()
					defer s.Close()
					testf(b, size, true, rand, s, c)
				})
			}
		})
	}
}
