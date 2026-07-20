package wszero_test

import (
	"compress/flate"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/simonfxr/wszero"
)

func BenchmarkCompressWriteMessage(b *testing.B) {
	for _, size := range []int{64, 256, 1024, 4096, 16384, 65536} {
		for _, level := range []int{flate.HuffmanOnly, flate.BestSpeed, flate.DefaultCompression} {
			levelName := "huffman"
			switch level {
			case flate.BestSpeed:
				levelName = "speed"
			case flate.DefaultCompression:
				levelName = "default"
			}
			b.Run(fmt.Sprintf("size=%d/level=%s", size, levelName), func(b *testing.B) {
				b.ReportAllocs()
				bp := wszero.NewBufferPool()
				c, s := compressedWsPair(
					wszero.ConnOpts{CompressionLevel: level, BufferPool: bp},
					wszero.ConnOpts{BufferPool: bp},
				)
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

				// Use compressible data (realistic for text/JSON workloads)
				msg := []byte(strings.Repeat("the quick brown fox jumps over the lazy dog. ", size/46+1))[:size]

				b.ResetTimer()
				b.SetBytes(int64(size))
				for i := 0; i < b.N; i++ {
					c.WriteMessage(wszero.BinaryMessage, msg)
				}
				b.StopTimer()
			})
		}
	}
}

func BenchmarkCompressWriteMessageRandom(b *testing.B) {
	// Incompressible data - worst case for compression
	for _, size := range []int{64, 1024, 16384} {
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			bp := wszero.NewBufferPool()
			c, s := compressedWsPair(
				wszero.ConnOpts{BufferPool: bp},
				wszero.ConnOpts{BufferPool: bp},
			)
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

			b.ResetTimer()
			b.SetBytes(int64(size))
			for i := 0; i < b.N; i++ {
				c.WriteMessage(wszero.BinaryMessage, msg)
			}
			b.StopTimer()
		})
	}
}

func BenchmarkCompressReadMessage(b *testing.B) {
	for _, size := range []int{64, 256, 1024, 4096, 16384, 65536} {
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			bp := wszero.NewBufferPool()
			c, s := compressedWsPair(
				wszero.ConnOpts{BufferPool: bp},
				wszero.ConnOpts{BufferPool: bp},
			)
			defer c.Close()
			defer s.Close()

			// Use compressible data
			msg := []byte(strings.Repeat("the quick brown fox jumps over the lazy dog. ", size/46+1))[:size]

			// Pre-send all messages
			done := make(chan struct{})
			go func() {
				defer close(done)
				for i := 0; i < b.N; i++ {
					if err := c.WriteMessage(wszero.BinaryMessage, msg); err != nil {
						return
					}
				}
			}()

			b.ResetTimer()
			b.SetBytes(int64(size))
			for i := 0; i < b.N; i++ {
				_, data, err := s.ReadMessage()
				if err != nil {
					b.Fatal(err)
				}
				bp.PutBuffer(data)
			}
			b.StopTimer()
			<-done
		})
	}
}

func BenchmarkCompressVsNoCompress(b *testing.B) {
	// Compare compressed vs uncompressed write throughput
	size := 4096
	msg := []byte(strings.Repeat("websocket message payload data ", size/31+1))[:size]

	b.Run("no-compression", func(b *testing.B) {
		b.ReportAllocs()
		bp := wszero.NewBufferPool()
		cnc, snc := connPair()
		c := (wszero.ConnOpts{BufferPool: bp}).NewConn(cnc, true)
		s := (wszero.ConnOpts{BufferPool: bp}).NewConn(snc, false)
		defer c.Close()
		defer s.Close()

		go func() {
			f, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
			defer f.Close()
			_, _ = io.CopyBuffer(f, s.NetConn(), make([]byte, 1<<16))
		}()

		b.ResetTimer()
		b.SetBytes(int64(size))
		for i := 0; i < b.N; i++ {
			c.WriteMessage(wszero.BinaryMessage, msg)
		}
		b.StopTimer()
	})

	b.Run("compression", func(b *testing.B) {
		b.ReportAllocs()
		bp := wszero.NewBufferPool()
		c, s := compressedWsPair(
			wszero.ConnOpts{BufferPool: bp},
			wszero.ConnOpts{BufferPool: bp},
		)
		defer c.Close()
		defer s.Close()

		go func() {
			f, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
			defer f.Close()
			_, _ = io.CopyBuffer(f, s.NetConn(), make([]byte, 1<<16))
		}()

		b.ResetTimer()
		b.SetBytes(int64(size))
		for i := 0; i < b.N; i++ {
			c.WriteMessage(wszero.BinaryMessage, msg)
		}
		b.StopTimer()
	})
}
