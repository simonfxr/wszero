package wszero

import (
	"bytes"
	"compress/flate"
	"io"
	"net/http"
	"strings"
	"sync"
)

const (
	// minCompressionLevel is the minimum compression level (Huffman only).
	minCompressionLevel = flate.HuffmanOnly
	// maxCompressionLevel is the maximum compression level.
	maxCompressionLevel = flate.BestCompression
	// DefaultCompressionLevel is the default compression level used when
	// compression is enabled but no explicit level is set.
	DefaultCompressionLevel = flate.BestSpeed

	// flateTail is the sync flush tail that is stripped from compressed
	// messages per RFC 7692.
	flateTailLen = 4
)

// flateTailFull is the tail appended during decompression: the 4-byte sync
// flush marker followed by a final empty DEFLATE stored block. The final block
// signals EOF to the flate reader.
var flateTailFull = [...]byte{
	0x00, 0x00, 0xff, 0xff, // sync flush marker (stripped on write, re-added on read)
	0x01, 0x00, 0x00, 0xff, 0xff, // final empty stored block (BFINAL=1, BTYPE=00, LEN=0)
}

// flateReader combines the interfaces needed from a pooled flate reader.
type flateReader interface {
	io.ReadCloser
	flate.Resetter
}

// flateWriterPools contains sync.Pools keyed by compression level.
var flateWriterPools [maxCompressionLevel - minCompressionLevel + 1]sync.Pool

// flateReaderPool pools flate.Reader instances.
var flateReaderPool = sync.Pool{
	New: func() any { return flate.NewReader(nil).(flateReader) },
}

func getFlateWriter(level int) *flate.Writer {
	p := &flateWriterPools[level-minCompressionLevel]
	if fw, _ := p.Get().(*flate.Writer); fw != nil {
		return fw
	}
	fw, _ := flate.NewWriter(nil, level)
	return fw
}

func putFlateWriter(fw *flate.Writer, level int) {
	p := &flateWriterPools[level-minCompressionLevel]
	p.Put(fw)
}

func getFlateReader() flateReader {
	return flateReaderPool.Get().(flateReader)
}

func putFlateReader(fr flateReader) {
	flateReaderPool.Put(fr)
}

// compressMessage compresses data using DEFLATE and strips the sync flush
// trailer per RFC 7692. It uses pooled flate.Writers. The returned buffer is
// obtained from bp if non-nil.
func compressMessage(data []byte, level int, bp BufferPool) ([]byte, error) {
	// Estimate: compressed should be smaller, but use data len as initial cap
	initCap := max(len(data), 64)

	var buf []byte
	if bp != nil {
		buf = bp.GetBuffer(initCap)
	}
	if buf == nil {
		buf = make([]byte, 0, initCap)
	} else {
		buf = buf[:0]
	}

	w := &bytesWriter{buf: buf}
	fw := getFlateWriter(level)
	fw.Reset(w)

	if _, err := fw.Write(data); err != nil {
		putFlateWriter(fw, level)
		if bp != nil {
			bp.PutBuffer(w.buf)
		}
		return nil, err
	}

	if err := fw.Flush(); err != nil {
		putFlateWriter(fw, level)
		if bp != nil {
			bp.PutBuffer(w.buf)
		}
		return nil, err
	}

	putFlateWriter(fw, level)

	// Strip the 4-byte sync flush trailer [0x00, 0x00, 0xff, 0xff]
	out := w.buf
	if len(out) >= flateTailLen {
		out = out[:len(out)-flateTailLen]
	}

	return out, nil
}

// decompressMessage decompresses a permessage-deflate payload. The input is the
// compressed bytes (without the 4-byte trailer). Returns decompressed data
// using bp if non-nil. The readLimit bounds the decompressed size.
func decompressMessage(data []byte, readLimit int, bp BufferPool) ([]byte, error) {
	// Append the full tail (sync flush + final block) for the flate reader
	src := io.MultiReader(
		bytes.NewReader(data),
		bytes.NewReader(flateTailFull[:]),
	)

	fr := getFlateReader()
	fr.Reset(src, nil)

	// Initial estimate: decompressed is typically 2-10x larger
	initCap := min(max(len(data)*3, 256), readLimit)

	var out []byte
	if bp != nil {
		out = bp.GetBuffer(initCap)
	}
	if out == nil {
		out = make([]byte, 0, initCap)
	} else {
		// Cap the slice to initCap so growth logic works correctly even when
		// the pool returns a larger buffer.
		out = out[:0:initCap]
	}

	for {
		if len(out) == cap(out) {
			// Need to grow
			newCap := min(cap(out)*2, readLimit)
			if newCap <= cap(out) {
				// Would exceed read limit — probe for one more byte
				var probe [1]byte
				n, err := fr.Read(probe[:])
				if n > 0 {
					putFlateReader(fr)
					if bp != nil {
						bp.PutBuffer(out)
					}
					return nil, ErrReadLimit
				}
				if err == io.EOF {
					break
				}
				if err != nil {
					putFlateReader(fr)
					if bp != nil {
						bp.PutBuffer(out)
					}
					return nil, err
				}
				continue
			}
			var newBuf []byte
			if bp != nil {
				newBuf = bp.GetBuffer(newCap)
			}
			if newBuf == nil {
				newBuf = make([]byte, len(out), newCap)
			} else {
				newBuf = newBuf[:len(out):newCap]
			}
			copy(newBuf, out)
			if bp != nil {
				bp.PutBuffer(out)
			}
			out = newBuf
		}

		n, err := fr.Read(out[len(out):cap(out)])
		out = out[:len(out)+n]
		if err == io.EOF {
			break
		}
		if err != nil {
			putFlateReader(fr)
			if bp != nil {
				bp.PutBuffer(out)
			}
			return nil, err
		}
	}

	putFlateReader(fr)
	return out, nil
}

// bytesWriter is a minimal io.Writer backed by a growing []byte.
type bytesWriter struct {
	buf []byte
}

func (w *bytesWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	return len(p), nil
}

// negotiateCompression checks if the client offered permessage-deflate in their
// request headers. Returns true if we should accept compression.
func negotiateCompression(h http.Header) bool {
	for _, ext := range h["Sec-Websocket-Extensions"] {
		for offer := range strings.SplitSeq(ext, ",") {
			offer = strings.TrimSpace(offer)
			name, _, _ := strings.Cut(offer, ";")
			if strings.TrimSpace(name) == "permessage-deflate" {
				return true
			}
		}
	}
	return false
}

// parseAcceptedCompression checks if the server accepted permessage-deflate.
func parseAcceptedCompression(h http.Header) bool {
	for _, ext := range h["Sec-Websocket-Extensions"] {
		for offer := range strings.SplitSeq(ext, ",") {
			offer = strings.TrimSpace(offer)
			name, _, _ := strings.Cut(offer, ";")
			if strings.TrimSpace(name) == "permessage-deflate" {
				return true
			}
		}
	}
	return false
}
