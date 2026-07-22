package interop_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/simonfxr/wszero"
	"github.com/stretchr/testify/assert"
)

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
