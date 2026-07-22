package wszero_test

import (
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/simonfxr/wszero"
	"github.com/stretchr/testify/assert"
)

func TestFormatCloseMessage(t *testing.T) {
	tests := []struct {
		code   int
		reason string
		want   []byte
	}{
		{wszero.CloseNormalClosure, "", []byte{0x03, 0xe8}},
		{wszero.CloseGoingAway, "bye", []byte{0x03, 0xe9, 'b', 'y', 'e'}},
		{wszero.CloseNoStatusReceived, "", []byte{}},
		{wszero.CloseProtocolError, "error", []byte{0x03, 0xea, 'e', 'r', 'r', 'o', 'r'}},
	}
	for _, tt := range tests {
		got := wszero.FormatCloseMessage(tt.code, tt.reason)
		assert.Equal(t, tt.want, got)
	}
}

func TestIsCloseError(t *testing.T) {
	closeErr := &wszero.CloseError{Code: wszero.CloseNormalClosure, Text: "bye"}
	assert.True(t, wszero.IsCloseError(closeErr, wszero.CloseNormalClosure))
	assert.True(t, wszero.IsCloseError(closeErr, wszero.CloseGoingAway, wszero.CloseNormalClosure))
	assert.False(t, wszero.IsCloseError(closeErr, wszero.CloseGoingAway))
	assert.False(t, wszero.IsCloseError(errors.New("other"), wszero.CloseNormalClosure))
	assert.False(t, wszero.IsCloseError(fmt.Errorf("wrapped: %w", closeErr), wszero.CloseGoingAway))
	assert.True(t, wszero.IsCloseError(fmt.Errorf("wrapped: %w", closeErr), wszero.CloseNormalClosure))
}

func TestIsUnexpectedCloseError(t *testing.T) {
	closeErr := &wszero.CloseError{Code: wszero.CloseProtocolError, Text: "bad"}
	assert.True(t, wszero.IsUnexpectedCloseError(closeErr, wszero.CloseNormalClosure))
	assert.True(t, wszero.IsUnexpectedCloseError(closeErr, wszero.CloseNormalClosure, wszero.CloseGoingAway))
	assert.False(t, wszero.IsUnexpectedCloseError(closeErr, wszero.CloseProtocolError))
	assert.False(t, wszero.IsUnexpectedCloseError(errors.New("other"), wszero.CloseNormalClosure))
	assert.True(t, wszero.IsUnexpectedCloseError(fmt.Errorf("wrapped: %w", closeErr), wszero.CloseNormalClosure))
}

func connPair() (net.Conn, net.Conn) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		panic(err)
	}
	cnc, err := net.FileConn(os.NewFile(uintptr(fds[0]), "__ws_c_sock_nc__"))
	if err != nil {
		panic(err)
	}
	snc, err := net.FileConn(os.NewFile(uintptr(fds[1]), "__ws_s_sock_nc__"))
	if err != nil {
		panic(err)
	}
	return cnc, snc
}

func wsPair(co, so wszero.ConnOpts) (c *wszero.Conn, s *wszero.Conn) {
	cnc, snc := connPair()
	return co.NewConn(cnc, true), so.NewConn(snc, false)
}

func putbuf(c *wszero.Conn, d []byte) {
	if bp := c.BufferPool(); bp != nil {
		bp.PutBuffer(d)
	}
}

type spyBufferPool struct {
	buf  []byte
	gets int
	puts int
}

func (s *spyBufferPool) GetBuffer(n int) []byte {
	s.gets++
	if cap(s.buf) < n {
		s.buf = make([]byte, max(n, 1024))
	}
	return s.buf[:cap(s.buf)]
}

func (s *spyBufferPool) PutBuffer([]byte) bool {
	s.puts++
	return true
}

func TestWriteJSON(t *testing.T) {
	type message struct {
		Message string `json:"message"`
		HTML    string `json:"html"`
		Count   int    `json:"count"`
	}

	a := assert.New(t)
	want := message{Message: "hello", HTML: "<b>bold</b>", Count: 7}
	c, s := wsPair(wszero.ConnOpts{}, wszero.ConnOpts{})
	defer c.Close()
	defer s.Close()

	err := c.WriteJSON(want)
	a.NoError(err)
	mt, data, err := s.ReadMessage()
	a.NoError(err)
	a.Equal(wszero.TextMessage, mt)
	a.Equal([]byte("{\"message\":\"hello\",\"html\":\"\\u003cb\\u003ebold\\u003c/b\\u003e\",\"count\":7}"), data)
	putbuf(s, data)
}

func TestReadJSON(t *testing.T) {
	type message struct {
		Message string `json:"message"`
		Count   int    `json:"count"`
	}

	a := assert.New(t)
	c, s := wsPair(wszero.ConnOpts{}, wszero.ConnOpts{})
	defer c.Close()
	defer s.Close()

	err := c.WriteMessage(wszero.TextMessage, []byte("{\"message\":\"hello\",\"count\":7}"))
	a.NoError(err)

	var got message
	err = s.ReadJSON(&got)
	a.NoError(err)
	a.Equal(message{Message: "hello", Count: 7}, got)
}

func TestReadJSONReturnsBufferPoolData(t *testing.T) {
	a := assert.New(t)
	bp := &spyBufferPool{}
	c, s := wsPair(wszero.ConnOpts{}, wszero.ConnOpts{BufferPool: bp})
	defer c.Close()
	defer s.Close()

	err := c.WriteMessage(wszero.TextMessage, []byte("{\"message\":\"hello\"}"))
	a.NoError(err)

	var got struct {
		Message string `json:"message"`
	}
	err = s.ReadJSON(&got)
	a.NoError(err)
	a.Equal("hello", got.Message)
	a.Equal(1, bp.gets)
	a.Equal(1, bp.puts)
}

func wsHeaders() http.Header {
	h := http.Header{}
	h.Set("Connection", "Upgrade")
	h.Set("Upgrade", "websocket")
	h.Set("Sec-Websocket-Version", "13")
	h.Set("Sec-Websocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	return h
}

func TestUpgraderCheckOriginDefault(t *testing.T) {
	a := assert.New(t)
	upgrader := &wszero.Upgrader{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			// error already written by Upgrade or we write it
			if w.Header().Get("Content-Type") == "" {
				http.Error(w, err.Error(), http.StatusBadRequest)
			}
		}
	}))
	defer ts.Close()

	// Same origin → allowed
	req, _ := http.NewRequest("GET", ts.URL+"/ws", nil)
	maps.Copy(req.Header, wsHeaders())
	req.Header.Set("Origin", ts.URL)
	resp, err := ts.Client().Do(req)
	a.NoError(err)
	a.Equal(http.StatusSwitchingProtocols, resp.StatusCode)
	resp.Body.Close()

	// No origin → allowed
	req, _ = http.NewRequest("GET", ts.URL+"/ws", nil)
	maps.Copy(req.Header, wsHeaders())
	resp, err = ts.Client().Do(req)
	a.NoError(err)
	a.Equal(http.StatusSwitchingProtocols, resp.StatusCode)
	resp.Body.Close()

	// Different origin → rejected
	req, _ = http.NewRequest("GET", ts.URL+"/ws", nil)
	maps.Copy(req.Header, wsHeaders())
	req.Header.Set("Origin", "http://evil.com")
	resp, err = ts.Client().Do(req)
	a.NoError(err)
	a.Equal(http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()
}

func TestUpgraderCheckOriginCustom(t *testing.T) {
	a := assert.New(t)

	allowed := []string{"http://allowed.com", "http://also-ok.com"}
	upgrader := &wszero.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			for _, a := range allowed {
				if strings.EqualFold(origin, a) {
					return true
				}
			}
			return false
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			if w.Header().Get("Content-Type") == "" {
				http.Error(w, err.Error(), http.StatusBadRequest)
			}
		}
	}))
	defer ts.Close()

	// Allowed origin
	req, _ := http.NewRequest("GET", ts.URL+"/ws", nil)
	maps.Copy(req.Header, wsHeaders())
	req.Header.Set("Origin", "http://allowed.com")
	resp, err := ts.Client().Do(req)
	a.NoError(err)
	a.Equal(http.StatusSwitchingProtocols, resp.StatusCode)
	resp.Body.Close()

	// Disallowed origin
	req, _ = http.NewRequest("GET", ts.URL+"/ws", nil)
	maps.Copy(req.Header, wsHeaders())
	req.Header.Set("Origin", "http://evil.com")
	resp, err = ts.Client().Do(req)
	a.NoError(err)
	a.Equal(http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	// No origin → also rejected by custom callback (doesn't match allowed list)
	req, _ = http.NewRequest("GET", ts.URL+"/ws", nil)
	maps.Copy(req.Header, wsHeaders())
	resp, err = ts.Client().Do(req)
	a.NoError(err)
	a.Equal(http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()
}

func TestUpgraderCheckOriginAllowAll(t *testing.T) {
	a := assert.New(t)

	upgrader := &wszero.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			if w.Header().Get("Content-Type") == "" {
				http.Error(w, err.Error(), http.StatusBadRequest)
			}
		}
	}))
	defer ts.Close()

	// Any origin allowed
	req, _ := http.NewRequest("GET", ts.URL+"/ws", nil)
	maps.Copy(req.Header, wsHeaders())
	req.Header.Set("Origin", "http://anything.example.org")
	resp, err := ts.Client().Do(req)
	a.NoError(err)
	a.Equal(http.StatusSwitchingProtocols, resp.StatusCode)
	resp.Body.Close()
}
