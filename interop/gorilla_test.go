package interop_test

import (
	"errors"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

func newWebsocketDialer(transp *http.Transport) dialer[*websocket.Conn] {
	dialer := dup(websocket.DefaultDialer)
	dialer.NetDialContext = transp.DialContext
	dialer.TLSClientConfig = transp.TLSClientConfig
	dialer.WriteBufferPool = &sync.Pool{}
	return dialer
}

var websocketUpgrader = &websocket.Upgrader{
	WriteBufferPool: &sync.Pool{},
}

var websocketType wsconn = (*websocket.Conn)(nil)

// gorillaSetPongHandler sets a pong handler on a gorilla websocket.Conn.
func gorillaSetPongHandler(c wsconn, onPong func(data []byte) error) bool {
	if cc, ok := c.(*websocket.Conn); ok {
		cc.SetPongHandler(func(appData string) error {
			return onPong([]byte(appData))
		})
		return true
	}
	return false
}

// gorillaSetCloseHandler sets a close handler on a gorilla websocket.Conn.
func gorillaSetCloseHandler(c wsconn, handler func(code int, text string)) bool {
	if cc, ok := c.(*websocket.Conn); ok {
		cc.SetCloseHandler(func(code int, text string) error {
			handler(code, text)
			return nil
		})
		return true
	}
	return false
}

// gorillaCloseErrorInfo extracts close code/text from a gorilla CloseError.
func gorillaCloseErrorInfo(err error) (code int, text string, ok bool) {
	var ce *websocket.CloseError
	if errors.As(err, &ce) {
		return ce.Code, ce.Text, true
	}
	return 0, "", false
}

// gorillaIsCloseError checks if err is a gorilla CloseError.
func gorillaIsCloseError(err error) bool {
	var ce *websocket.CloseError
	return errors.As(err, &ce)
}

// gorillaIsCloseSent checks if err is websocket.ErrCloseSent.
func gorillaIsCloseSent(err error) bool {
	return errors.Is(err, websocket.ErrCloseSent)
}
