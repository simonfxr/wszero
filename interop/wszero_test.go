package interop_test

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/simonfxr/wszero"
	"github.com/stretchr/testify/assert"
)

var wsAnyHandshakes = []handshake[wsconn, wsconn]{
	{"wszero;wszero", genDialer(newDialer), wsType, genUpgrader(&wszero.Upgrader{}), wsType, nil},
	{"wszero;websocket", genDialer(newDialer), wsType, genUpgrader(websocketUpgrader), websocketType, nil},
	{"websocket;wszero", genDialer(newWebsocketDialer), websocketType, genUpgrader(&wszero.Upgrader{}), wsType, nil},
	{"wszero;gobwas", genDialer(newDialer), wsType, genUpgrader(&GobwasUpgrader{}), gobwasType, nil},
	{"gobwas;wszero", genDialer(newGobwasDialer), gobwasType, genUpgrader(&wszero.Upgrader{}), wsType, nil},
}

var clientHandshakes = []handshake[wsconn, wsconn]{
	{"wszero", genDialer(newDialer), wsType, genUpgrader(&wszero.Upgrader{}), wsType, nil},
	{"websocket", genDialer(newWebsocketDialer), websocketType, genUpgrader(&wszero.Upgrader{}), wsType, nil},
	{"gobwas", genDialer(newGobwasDialer), gobwasType, genUpgrader(&wszero.Upgrader{}), wsType, nil},
}

var benchClientHandshakes = append(clientHandshakes,
	handshake[wsconn, wsconn]{"nhooyr", genDialer(newNhooyrDialer), nhooyrType, genUpgrader(&wszero.Upgrader{}), wsType, nil},
)

var serverHandshakes = []handshake[wsconn, wsconn]{
	{"wszero", genDialer(newDialer), wsType, genUpgrader(&wszero.Upgrader{}), wsType, nil},
	{"websocket", genDialer(newDialer), wsType, genUpgrader(websocketUpgrader), websocketType, nil},
	{"gobwas", genDialer(newDialer), wsType, genUpgrader(&GobwasUpgrader{}), gobwasType, nil},
}

var benchServerHandshakes = append(serverHandshakes,
	handshake[wsconn, wsconn]{"nhooyr", genDialer(newDialer), wsType, genUpgrader(&NhooyrUpgrader{}), nhooyrType, nil},
)

func TestEcho(t *testing.T) {
	for _, size := range []int{wszero.MinReadBufferSize, 512, 1024, 4096} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			foreachHandshakes(t, "", wsAnyHandshakes, bufPoolVariants, func(t *testing.T, h handshake[wsconn, wsconn]) {
				a := assert.New(t)
				c, s := wsHandshakePair(h.d, h.u)
				defer c.Close()
				defer s.Close()
				h.prepare(c, s)

				xbytes := make([]byte, 70000)
				_, _ = rand.Read(xbytes)
				longmsg := base64.RawURLEncoding.EncodeToString(xbytes)

				for _, msg := range []string{
					"",
					"abc",
					"F2YDnQRdDFJcfVfGn3EQd1TVXzVqewD6bhSZFFRdDbdJ3APUIHApCIe286no9FusEmX8yTCfb06FNnCgHTdapWBqU6pOWVHg2O28JKMErE08Jpb4UI18De0x6B078X",
					longmsg,
				} {
					eqSlice := func(expected, got []byte) {
						if len(expected) == 0 {
							expected = nil
						}
						if len(got) == 0 {
							got = nil
						}
						a.Equal(expected, got)
					}
					setBuf := func(s wsconn, buf int) {
						if s, _ := s.(*wszero.Conn); s != nil {
							s.SetReadBuffer(make([]byte, buf))
						}
					}
					setBuf(c, size)
					setBuf(s, size)

					err := writeMessageString(c, wszero.BinaryMessage, msg)
					a.NoError(err)
					mt, data, err := s.ReadMessage()
					a.NoError(err)
					a.Equal(wszero.BinaryMessage, mt)
					eqSlice([]byte(msg), data)
					putbuf(s, data)

					err = writeMessageString(s, wszero.TextMessage, string(data))
					a.NoError(err)

					mt, data, err = c.ReadMessage()
					a.NoError(err)
					a.Equal(wszero.TextMessage, mt)
					eqSlice([]byte(msg), data)
					putbuf(c, data)
				}
			})
		})
	}
}

func TestPingPong(t *testing.T) {
	foreachHandshakes(t, "", wsAnyHandshakes, nil, func(t *testing.T, h handshake[wsconn, wsconn]) {
		if strings.Contains(t.Name(), "gobwas") {
			return
		}

		a := assert.New(t)
		c, s := wsHandshakePair(h.d, h.u)
		defer c.Close()
		defer s.Close()
		h.prepare(c, s)
		done := make(chan struct{})

		go func() {
			defer func() { close(done) }()
			for {
				_, d, err := s.ReadMessage()
				if err != nil {
					a.True(errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || gorillaIsCloseError(err))
					break
				}
				putbuf(s, d)
			}
		}()

		pongData := []byte(nil)
		if cc, ok := c.(*wszero.Conn); ok {
			cc.SetPongHandler(func(c *wszero.Conn, b []byte) error {
				pongData = make([]byte, len(b))
				copy(pongData, b)
				return c.Close()
			})
		} else {
			gorillaSetPongHandler(c, func(data []byte) error {
				pongData = make([]byte, len(data))
				copy(pongData, data)
				return c.Close()
			})
		}

		err := c.WriteControl(wszero.PingMessage, []byte("ping"), time.Time{})
		a.NoError(err)
		mt, data, err := c.ReadMessage()
		a.True(errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed))
		a.True(mt == 0 || mt == -1)
		a.True(len(data) == 0)
		putbuf(c, data)

		a.Equal([]byte("ping"), pongData)

		s.Close()
		<-done
	})
}

func TestCloseMessage(t *testing.T) {
	foreachHandshakes(t, "", wsAnyHandshakes, nil, func(t *testing.T, h handshake[wsconn, wsconn]) {
		a := assert.New(t)

		tests := []struct {
			code   int
			reason string
		}{
			{wszero.CloseNormalClosure, ""},
			{wszero.CloseGoingAway, "going away"},
			{wszero.CloseProtocolError, "protocol error"},
		}

		for _, tt := range tests {
			c, s := wsHandshakePair(h.d, h.u)
			defer c.Close()
			defer s.Close()
			h.prepare(c, s)

			err := c.WriteMessage(wszero.CloseMessage, wszero.FormatCloseMessage(tt.code, tt.reason))
			a.NoError(err)

			_, _, err = s.ReadMessage()
			a.Error(err)

			if _, ok := s.(*wszero.Conn); ok {
				a.True(wszero.IsCloseError(err, tt.code))
				var ce *wszero.CloseError
				if a.True(errors.As(err, &ce)) {
					a.Equal(tt.code, ce.Code)
					a.Equal(tt.reason, ce.Text)
				}
			} else if code, text, ok := gorillaCloseErrorInfo(err); ok {
				a.Equal(tt.code, code)
				a.Equal(tt.reason, text)
			}
		}
	})
}

func TestReadLimit(t *testing.T) {
	foreachHandshakes(t, "", wsAnyHandshakes, nil, func(t *testing.T, h handshake[wsconn, wsconn]) {
		if strings.Contains(t.Name(), "gobwas") {
			return
		}

		a := assert.New(t)
		c, s := wsHandshakePair(h.d, h.u)
		defer c.Close()
		defer s.Close()
		h.prepare(c, s)

		closeCode := -1
		switch sc := s.(type) {
		case *wszero.Conn:
			sc.SetCloseHandler(func(c *wszero.Conn, code int, text string) {
				closeCode = code
			})
		default:
			gorillaSetCloseHandler(sc, func(code int, text string) {
				closeCode = code
			})
		}

		err := s.WriteMessage(wszero.BinaryMessage, make([]byte, 1025))
		a.NoError(err)

		c.SetReadLimit(1024)
		_, d, err := c.ReadMessage()
		a.True(strings.Contains(err.Error(), "read limit exceeded"))
		putbuf(c, d)

		_, d, err = s.ReadMessage()
		switch s.(type) {
		case *wszero.Conn:
			var ce *wszero.CloseError
			a.ErrorAs(err, &ce)
		default:
			var ce *websocket.CloseError
			a.ErrorAs(err, &ce)
		}
		a.Equal(wszero.CloseMessageTooBig, closeCode)
		putbuf(s, d)

		err = c.WriteMessage(wszero.BinaryMessage, nil)
		a.True(errors.Is(err, net.ErrClosed) || gorillaIsCloseSent(err))

		_, d, err = c.ReadMessage()
		a.True(errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "read limit exceeded"))
		putbuf(c, d)
	})
}

func TestFragmentWriter(t *testing.T) {
	foreachHandshakes(t, "", wsAnyHandshakes, nil, func(t *testing.T, h handshake[wsconn, wsconn]) {
		a := assert.New(t)
		c0, s0 := wsHandshakePair(h.d, h.u)
		defer c0.Close()
		defer s0.Close()
		h.prepare(c0, s0)
		c, s := anyWs(c0, s0)

		fw := c.FrameWriter(wszero.BinaryMessage)
		msg := []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
		sizes := []int{1, 2, 3, 4, 5, 6, 7, 8, 11, 15}
		off := 0
		for _, n := range sizes {
			err := fw.Write(msg[off : off+n])
			a.NoError(err)
			off += n
		}

		err := fw.Close()
		a.NoError(err)

		rmt, rm, err := s.ReadMessage()
		a.NoError(err)
		a.Equal(wszero.BinaryMessage, rmt)
		a.Equal(msg, rm)
		putbuf(s, rm)

		fw.Reset(c, wszero.BinaryMessage)
		fw.WriteString("abc")
		fw.Final()
		fw.WriteString("def")
		fw.Close()

		rmt, rm, err = s.ReadMessage()
		a.NoError(err)
		a.Equal(wszero.BinaryMessage, rmt)
		a.Equal([]byte("abcdef"), rm)
		putbuf(s, rm)

		fw.Reset(c, wszero.BinaryMessage)
		bufs := net.Buffers{[]byte("foo"), []byte("bar")}
		fw.WriteBuffers(&bufs)
		fw.WriteString("baz")
		fw.Final()
		fw.Close()

		rmt, rm, err = s.ReadMessage()
		a.NoError(err)
		a.Equal(wszero.BinaryMessage, rmt)
		a.Equal([]byte("foobarbaz"), rm)
		putbuf(s, rm)
	})
}

func TestBadProtocol(t *testing.T) {
	foreachHandshakes(t, "", wsAnyHandshakes, bufPoolVariants, func(t *testing.T, h handshake[wsconn, wsconn]) {
		for _, scenario := range []struct {
			name string
			msg  [128]byte
		}{
			{name: "invalid rsv1", msg: [128]byte{
				0x92, 0x80,
				0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF,
				0x88, 0x77, 0x66, 0x55, 0x44, 0x33,
			}},
			{name: "invalid rsv2", msg: [128]byte{
				0xA2, 0x80,
				0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF,
				0x88, 0x77, 0x66, 0x55, 0x44, 0x33,
			}},
			{name: "invalid rsv3", msg: [128]byte{
				0xC2, 0x80,
				0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF,
				0x88, 0x77, 0x66, 0x55, 0x44, 0x33,
			}},
			{name: "continuation op", msg: [128]byte{
				0x00, 0x00,
				0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF,
				0x88, 0x77, 0x66, 0x55, 0x44, 0x33,
			}},
			{name: "invalid cont control frame", msg: [128]byte{
				0x0A, 0x00,
				0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF,
				0x88, 0x77, 0x66, 0x55, 0x44, 0x33,
			}},
			{name: "invalid control op", msg: [128]byte{
				0x8F, 0x00,
				0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF,
				0x88, 0x77, 0x66, 0x55, 0x44, 0x33,
			}},

			{name: "invalid second frame", msg: [128]byte{
				0x02, 0x00,
				0x07, 0x00,
				0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF,
				0x88, 0x77, 0x66, 0x55, 0x44, 0x33,
			}},
		} {
			t.Run(scenario.name, func(t *testing.T) {
				a := assert.New(t)
				c0, s0 := wsHandshakePair(h.d, h.u)
				defer c0.Close()
				defer s0.Close()
				h.prepare(c0, s0)
				c, s := anyWs(c0, s0)

				_, err := s.NetConn().Write(scenario.msg[:])
				a.NoError(err)

				mt, d, err := c.ReadMessage()
				a.ErrorIs(err, wszero.ErrProtocol)
				a.Equal(0, mt)
				a.Equal([]byte(nil), d)
				putbuf(c, d)
			})
		}
	})
}
