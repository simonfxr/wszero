package wszero_test

import (
	"compress/flate"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/simonfxr/wszero"
	"github.com/stretchr/testify/assert"
)

func compressedWsPair(co, so wszero.ConnOpts) (c *wszero.Conn, s *wszero.Conn) {
	co.EnableCompression = true
	so.EnableCompression = true
	cnc, snc := connPair()
	// Simulate both sides having negotiated compression
	c = co.NewCompressedConn(cnc, true)
	s = so.NewCompressedConn(snc, false)
	return c, s
}

func TestCompressEchoSimple(t *testing.T) {
	a := assert.New(t)
	c, s := compressedWsPair(wszero.ConnOpts{}, wszero.ConnOpts{})
	defer c.Close()
	defer s.Close()

	a.True(c.CompressionEnabled())
	a.True(s.CompressionEnabled())

	msg := "hello world, this is a test message for compression"
	err := c.WriteMessage(wszero.TextMessage, []byte(msg))
	a.NoError(err)

	mt, data, err := s.ReadMessage()
	a.NoError(err)
	a.Equal(wszero.TextMessage, mt)
	a.Equal(msg, string(data))
	putbuf(s, data)
}

func TestCompressEchoVariousSizes(t *testing.T) {
	for _, size := range []int{0, 1, 10, 125, 126, 127, 200, 1000, 4096, 65536, 100000} {
		t.Run(fmt.Sprintf("size=%d", size), func(t *testing.T) {
			a := assert.New(t)
			c, s := compressedWsPair(wszero.ConnOpts{}, wszero.ConnOpts{})
			defer c.Close()
			defer s.Close()

			// Generate somewhat compressible data
			msg := make([]byte, size)
			for i := range msg {
				msg[i] = byte(i % 251)
			}

			err := c.WriteMessage(wszero.BinaryMessage, msg)
			a.NoError(err)

			mt, data, err := s.ReadMessage()
			a.NoError(err)
			a.Equal(wszero.BinaryMessage, mt)
			a.Equal(msg, data)
			putbuf(s, data)

			// Echo back
			err = s.WriteMessage(wszero.BinaryMessage, data)
			a.NoError(err)

			mt, data, err = c.ReadMessage()
			a.NoError(err)
			a.Equal(wszero.BinaryMessage, mt)
			a.Equal(msg, data)
			putbuf(c, data)
		})
	}
}

func TestCompressWithRandomData(t *testing.T) {
	a := assert.New(t)
	c, s := compressedWsPair(wszero.ConnOpts{}, wszero.ConnOpts{})
	defer c.Close()
	defer s.Close()

	// Random data (hard to compress)
	msg := make([]byte, 8192)
	_, _ = rand.Read(msg)

	err := c.WriteMessage(wszero.BinaryMessage, msg)
	a.NoError(err)

	mt, data, err := s.ReadMessage()
	a.NoError(err)
	a.Equal(wszero.BinaryMessage, mt)
	a.Equal(msg, data)
	putbuf(s, data)
}

func TestCompressWithBufferPool(t *testing.T) {
	a := assert.New(t)
	bp := wszero.NewBufferPool()
	c, s := compressedWsPair(
		wszero.ConnOpts{BufferPool: bp},
		wszero.ConnOpts{BufferPool: bp},
	)
	defer c.Close()
	defer s.Close()

	msg := strings.Repeat("hello world ", 100)
	err := c.WriteMessage(wszero.TextMessage, []byte(msg))
	a.NoError(err)

	mt, data, err := s.ReadMessage()
	a.NoError(err)
	a.Equal(wszero.TextMessage, mt)
	a.Equal(msg, string(data))
	bp.PutBuffer(data)
}

func TestCompressCompressionLevels(t *testing.T) {
	msg := strings.Repeat("ABCDEFGHIJKLMNOP", 64)

	for _, level := range []int{flate.HuffmanOnly, flate.BestSpeed, flate.DefaultCompression, flate.BestCompression} {
		t.Run("", func(t *testing.T) {
			a := assert.New(t)
			c, s := compressedWsPair(
				wszero.ConnOpts{CompressionLevel: level},
				wszero.ConnOpts{},
			)
			defer c.Close()
			defer s.Close()

			err := c.WriteMessage(wszero.TextMessage, []byte(msg))
			a.NoError(err)

			mt, data, err := s.ReadMessage()
			a.NoError(err)
			a.Equal(wszero.TextMessage, mt)
			a.Equal(msg, string(data))
			putbuf(s, data)
		})
	}
}

func TestCompressFrameWriter(t *testing.T) {
	a := assert.New(t)
	c, s := compressedWsPair(wszero.ConnOpts{}, wszero.ConnOpts{})
	defer c.Close()
	defer s.Close()

	fw := c.FrameWriter(wszero.BinaryMessage)
	fw.Write([]byte("hello "))
	fw.Write([]byte("world"))
	fw.Write([]byte(" compressed"))
	err := fw.Close()
	a.NoError(err)

	mt, data, err := s.ReadMessage()
	a.NoError(err)
	a.Equal(wszero.BinaryMessage, mt)
	a.Equal("hello world compressed", string(data))
	putbuf(s, data)
}

func TestCompressFrameWriterWithFinal(t *testing.T) {
	a := assert.New(t)
	c, s := compressedWsPair(wszero.ConnOpts{}, wszero.ConnOpts{})
	defer c.Close()
	defer s.Close()

	fw := c.FrameWriter(wszero.BinaryMessage)
	fw.Write([]byte("part1"))
	fw.Write([]byte("part2"))
	fw.Final()
	fw.Write([]byte("part3"))
	err := fw.Close()
	a.NoError(err)

	mt, data, err := s.ReadMessage()
	a.NoError(err)
	a.Equal(wszero.BinaryMessage, mt)
	a.Equal("part1part2part3", string(data))
	putbuf(s, data)
}

func TestCompressFrameWriterBuffers(t *testing.T) {
	a := assert.New(t)
	c, s := compressedWsPair(wszero.ConnOpts{}, wszero.ConnOpts{})
	defer c.Close()
	defer s.Close()

	fw := c.FrameWriter(wszero.BinaryMessage)
	bufs := net.Buffers{[]byte("foo"), []byte("bar"), []byte("baz")}
	_, err := fw.WriteBuffers(&bufs)
	a.NoError(err)
	err = fw.Close()
	a.NoError(err)

	mt, data, err := s.ReadMessage()
	a.NoError(err)
	a.Equal(wszero.BinaryMessage, mt)
	a.Equal("foobarbaz", string(data))
	putbuf(s, data)
}

func TestCompressControlFramesNotCompressed(t *testing.T) {
	a := assert.New(t)
	c, s := compressedWsPair(wszero.ConnOpts{}, wszero.ConnOpts{})
	defer c.Close()
	defer s.Close()

	// Write a data message from client
	err := c.WriteMessage(wszero.TextMessage, []byte("hello"))
	a.NoError(err)

	// Interleave a ping from client
	err = c.WriteControl(wszero.PingMessage, []byte("test"), time.Time{})
	a.NoError(err)

	// Write another data message
	err = c.WriteMessage(wszero.TextMessage, []byte("world"))
	a.NoError(err)

	// Server reads: should get first data message, handle ping transparently
	mt, data, err := s.ReadMessage()
	a.NoError(err)
	a.Equal(wszero.TextMessage, mt)
	a.Equal("hello", string(data))
	putbuf(s, data)

	// Second read gets second message (ping was handled inline)
	mt, data, err = s.ReadMessage()
	a.NoError(err)
	a.Equal(wszero.TextMessage, mt)
	a.Equal("world", string(data))
	putbuf(s, data)

	// Client should have received the pong by now (it was sent during server's read)
	pongReceived := false
	c.SetPongHandler(func(c *wszero.Conn, b []byte) error {
		pongReceived = true
		a.Equal([]byte("test"), b)
		return nil
	})

	// Send from server so client has something to read
	err = s.WriteMessage(wszero.TextMessage, []byte("reply"))
	a.NoError(err)

	mt, data, err = c.ReadMessage()
	a.NoError(err)
	a.Equal(wszero.TextMessage, mt)
	a.Equal("reply", string(data))
	a.True(pongReceived)
	putbuf(c, data)
}

func TestCompressReadLimit(t *testing.T) {
	a := assert.New(t)
	c, s := compressedWsPair(wszero.ConnOpts{}, wszero.ConnOpts{})
	defer c.Close()
	defer s.Close()

	// Write a large message that decompresses to more than the limit
	msg := strings.Repeat("X", 5000)
	err := c.WriteMessage(wszero.TextMessage, []byte(msg))
	a.NoError(err)

	// Set a read limit smaller than the decompressed size but larger than
	// MinReadBufferSize (the minimum enforced by SetReadLimit)
	s.SetReadLimit(1024)
	_, data, err := s.ReadMessage()
	a.Error(err)
	a.Contains(err.Error(), "read limit")
	putbuf(s, data)
}

func TestCompressHandshakeNegotiation(t *testing.T) {
	a := assert.New(t)

	// Both sides with compression enabled
	cDialer := func(transp *http.Transport) dialer[*wszero.Conn] {
		d := dup(wszero.DefaultDialer)
		d.Client = dup(d.Client)
		d.Client.Transport = transp
		d.EnableCompression = true
		return d
	}
	upgrader := &wszero.Upgrader{ConnOpts: wszero.ConnOpts{EnableCompression: true}}

	c, s := wsHandshakePair(
		func(t *http.Transport) dialer[*wszero.Conn] { return cDialer(t) },
		upgrader,
	)
	defer c.Close()
	defer s.Close()

	a.True(c.CompressionEnabled())
	a.True(s.CompressionEnabled())

	msg := "handshake compression test"
	err := c.WriteMessage(wszero.TextMessage, []byte(msg))
	a.NoError(err)

	mt, data, err := s.ReadMessage()
	a.NoError(err)
	a.Equal(wszero.TextMessage, mt)
	a.Equal(msg, string(data))
	putbuf(s, data)
}

func TestCompressHandshakeNotNegotiatedWhenServerDisabled(t *testing.T) {
	a := assert.New(t)

	cDialer := func(transp *http.Transport) dialer[*wszero.Conn] {
		d := dup(wszero.DefaultDialer)
		d.Client = dup(d.Client)
		d.Client.Transport = transp
		d.EnableCompression = true
		return d
	}

	// Server does NOT enable compression
	upgrader := &wszero.Upgrader{ConnOpts: wszero.ConnOpts{EnableCompression: false}}

	c, s := wsHandshakePair(
		func(t *http.Transport) dialer[*wszero.Conn] { return cDialer(t) },
		upgrader,
	)
	defer c.Close()
	defer s.Close()

	a.False(c.CompressionEnabled())
	a.False(s.CompressionEnabled())
}

func TestCompressHandshakeNotNegotiatedWhenClientDisabled(t *testing.T) {
	a := assert.New(t)

	cDialer := func(transp *http.Transport) dialer[*wszero.Conn] {
		d := dup(wszero.DefaultDialer)
		d.Client = dup(d.Client)
		d.Client.Transport = transp
		d.EnableCompression = false
		return d
	}

	upgrader := &wszero.Upgrader{ConnOpts: wszero.ConnOpts{EnableCompression: true}}

	c, s := wsHandshakePair(
		func(t *http.Transport) dialer[*wszero.Conn] { return cDialer(t) },
		upgrader,
	)
	defer c.Close()
	defer s.Close()

	a.False(c.CompressionEnabled())
	a.False(s.CompressionEnabled())
}

func TestCompressInteropGorillaClient(t *testing.T) {
	a := assert.New(t)

	gorillaDialer := func(transp *http.Transport) dialer[*websocket.Conn] {
		d := dup(websocket.DefaultDialer)
		d.NetDialContext = transp.DialContext
		d.TLSClientConfig = transp.TLSClientConfig
		d.EnableCompression = true
		return d
	}

	upgrader := &wszero.Upgrader{ConnOpts: wszero.ConnOpts{EnableCompression: true}}

	gc, ws := wsHandshakePair(
		func(t *http.Transport) dialer[*websocket.Conn] { return gorillaDialer(t) },
		upgrader,
	)
	defer gc.Close()
	defer ws.Close()

	a.True(ws.CompressionEnabled())

	// Gorilla client -> wszero server
	msg := strings.Repeat("gorilla to wszero ", 50)
	err := gc.WriteMessage(websocket.TextMessage, []byte(msg))
	a.NoError(err)

	mt, data, err := ws.ReadMessage()
	a.NoError(err)
	a.Equal(wszero.TextMessage, mt)
	a.Equal(msg, string(data))
	putbuf(ws, data)

	// wszero server -> gorilla client
	msg2 := strings.Repeat("wszero to gorilla ", 50)
	err = ws.WriteMessage(wszero.TextMessage, []byte(msg2))
	a.NoError(err)

	mt, data2, err := gc.ReadMessage()
	a.NoError(err)
	a.Equal(websocket.TextMessage, mt)
	a.Equal(msg2, string(data2))
}

func TestCompressInteropGorillaServer(t *testing.T) {
	a := assert.New(t)

	wszeroDialer := func(transp *http.Transport) dialer[*wszero.Conn] {
		d := dup(wszero.DefaultDialer)
		d.Client = dup(d.Client)
		d.Client.Transport = transp
		d.EnableCompression = true
		return d
	}

	gorillaUpgrader := &websocket.Upgrader{
		EnableCompression: true,
	}

	wc, gs := wsHandshakePair(
		func(t *http.Transport) dialer[*wszero.Conn] { return wszeroDialer(t) },
		gorillaUpgrader,
	)
	defer wc.Close()
	defer gs.Close()

	a.True(wc.CompressionEnabled())

	// wszero client -> gorilla server
	msg := strings.Repeat("wszero to gorilla server ", 50)
	err := wc.WriteMessage(wszero.TextMessage, []byte(msg))
	a.NoError(err)

	mt, data, err := gs.ReadMessage()
	a.NoError(err)
	a.Equal(websocket.TextMessage, mt)
	a.Equal(msg, string(data))

	// gorilla server -> wszero client
	msg2 := strings.Repeat("gorilla server to wszero ", 50)
	err = gs.WriteMessage(websocket.TextMessage, []byte(msg2))
	a.NoError(err)

	mt, data2, err := wc.ReadMessage()
	a.NoError(err)
	a.Equal(wszero.TextMessage, mt)
	a.Equal(msg2, string(data2))
	putbuf(wc, data2)
}

func TestCompressMultipleMessages(t *testing.T) {
	a := assert.New(t)
	c, s := compressedWsPair(wszero.ConnOpts{}, wszero.ConnOpts{})
	defer c.Close()
	defer s.Close()

	// Send many messages to ensure no_context_takeover works correctly
	// (each message must be independently decompressible)
	for i := range 50 {
		msg := strings.Repeat("message_"+base64.RawURLEncoding.EncodeToString([]byte{byte(i)})+" ", 20)
		err := c.WriteMessage(wszero.TextMessage, []byte(msg))
		a.NoError(err)

		mt, data, err := s.ReadMessage()
		a.NoError(err)
		a.Equal(wszero.TextMessage, mt)
		a.Equal(msg, string(data))
		putbuf(s, data)
	}
}

func TestCompressEmptyMessage(t *testing.T) {
	a := assert.New(t)
	c, s := compressedWsPair(wszero.ConnOpts{}, wszero.ConnOpts{})
	defer c.Close()
	defer s.Close()

	err := c.WriteMessage(wszero.TextMessage, []byte{})
	a.NoError(err)

	mt, data, err := s.ReadMessage()
	a.NoError(err)
	a.Equal(wszero.TextMessage, mt)
	a.Empty(data) // may be nil or empty slice
	putbuf(s, data)
}

// Use imports
var _ = (*http.Transport)(nil)
var _ time.Time
