package wszero_test

import (
	"context"
	"net"
	"net/http"
	"time"

	"nhooyr.io/websocket"
)

type NhooyrConn websocket.Conn

func (c *NhooyrConn) ReadMessage() (int, []byte, error) {
	mt, data, err := (*websocket.Conn)(c).Read(context.Background())
	return int(mt), data, err
}

func (c *NhooyrConn) WriteMessage(mt int, data []byte) error {
	return (*websocket.Conn)(c).Write(context.Background(), websocket.MessageType(mt), data)
}

func (c *NhooyrConn) WriteControl(mt int, data []byte, deadline time.Time) error {
	return c.WriteMessage(mt, data)
}

func (c *NhooyrConn) NetConn() net.Conn {
	return nil
}

func (c *NhooyrConn) SetReadLimit(rlim int64) {
	(*websocket.Conn)(c).SetReadLimit(rlim)
}

func (c *NhooyrConn) Close() error {
	return (*websocket.Conn)(c).CloseNow()
}

type NhooyrUpgrader websocket.AcceptOptions

func (u *NhooyrUpgrader) Upgrade(w http.ResponseWriter, r *http.Request, _ http.Header) (*NhooyrConn, error) {
	c, err := websocket.Accept(w, r, (*websocket.AcceptOptions)(u))
	return (*NhooyrConn)(c), err
}

type NhooyrDialer struct{ websocket.DialOptions }

var NhooyrDefaultDialer = &NhooyrDialer{}

func (d *NhooyrDialer) DialContext(ctx context.Context, url string, _ http.Header) (*NhooyrConn, *http.Response, error) {
	conn, resp, err := websocket.Dial(ctx, url, &d.DialOptions)
	return (*NhooyrConn)(conn), resp, err
}
