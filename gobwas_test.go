package wszero_test

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

type GobwasConn struct {
	nc    net.Conn
	state ws.State
}

func (c *GobwasConn) ReadMessage() (int, []byte, error) {
	data, op, err := wsutil.ReadData(c.nc, c.state)
	return int(op), data, err
}

func (c *GobwasConn) WriteMessage(t int, d []byte) error {
	return wsutil.WriteMessage(c.nc, c.state, ws.OpCode(t), d)
}

func (c *GobwasConn) WriteControl(t int, d []byte, _ time.Time) error { return c.WriteMessage(t, d) }

func (c *GobwasConn) NetConn() net.Conn { return c.nc }

func (c *GobwasConn) SetReadLimit(int64) {}

func (c *GobwasConn) Close() error { return c.nc.Close() }

type GobwasUpgrader struct{}

func (u *GobwasUpgrader) Upgrade(w http.ResponseWriter, r *http.Request, _ http.Header) (*GobwasConn, error) {
	nc, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		return nil, err
	}
	return &GobwasConn{nc, ws.StateServerSide}, nil
}

type GobwasDialer ws.Dialer

var GobwasDefaultDialer = (*GobwasDialer)(&ws.DefaultDialer)

func (d *GobwasDialer) DialContext(ctx context.Context, urlStr string, _ http.Header) (*GobwasConn, *http.Response, error) {
	nc, _, _, err := (*ws.Dialer)(d).Dial(ctx, urlStr)
	if err != nil {
		return nil, nil, err
	}
	return &GobwasConn{nc, ws.StateClientSide}, nil, nil
}
