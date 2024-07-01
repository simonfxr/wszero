package wszero_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/simonfxr/wszero"
	"github.com/stretchr/testify/assert"
)

type wsconn interface {
	ReadMessage() (int, []byte, error)
	WriteMessage(int, []byte) error
	WriteControl(int, []byte, time.Time) error
	NetConn() net.Conn
	SetReadLimit(int64)
	Close() error
}

type upgrader[C wsconn] interface {
	Upgrade(http.ResponseWriter, *http.Request, http.Header) (C, error)
}

type upgraderFunc func(http.ResponseWriter, *http.Request, http.Header) (wsconn, error)

func (f upgraderFunc) Upgrade(w http.ResponseWriter, r *http.Request, h http.Header) (wsconn, error) {
	return f(w, r, h)
}

type dialer[C wsconn] interface {
	DialContext(context.Context, string, http.Header) (C, *http.Response, error)
}

type dialerFunc func(context.Context, string, http.Header) (wsconn, *http.Response, error)

func (f dialerFunc) DialContext(ctx context.Context, url string, h http.Header) (wsconn, *http.Response, error) {
	return f(ctx, url, h)
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

type listenerFunc struct {
	AcceptF func() (net.Conn, error)
	CloseF  func() error
	AddrVal net.Addr
}

func (l listenerFunc) Accept() (net.Conn, error) { return l.AcceptF() }
func (l listenerFunc) Close() error              { return l.CloseF() }
func (l listenerFunc) Addr() net.Addr            { return l.AddrVal }

func wsHandshakePair[C, S wsconn](newDialer func(*http.Transport) dialer[C], upgrader upgrader[S]) (C, S) {
	cnc, snc := connPair()

	clientConn := snc
	closed := make(chan struct{})
	doClose := sync.Once{}
	listener := listenerFunc{
		AcceptF: func() (net.Conn, error) {
			if c := clientConn; c != nil {
				clientConn = nil
				return c, nil
			}
			<-closed
			return nil, net.ErrClosed
		},
		CloseF: func() error {
			doClose.Do(func() { close(closed) })
			return nil
		},
	}

	var sws S
	servErr := error(nil)
	hdone := make(chan struct{})
	sdone := make(chan struct{})

	server := http.Server{
		Addr: ":8080",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() { close(hdone) }()
			sws, servErr = upgrader.Upgrade(w, r, nil)
		}),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{testCert},
		},
	}
	usetls := useTLS
	if usetls {
		server.Addr = ":8443"
	}

	go func() {
		defer func() { close(sdone) }()
		if usetls {
			server.ServeTLS(listener, "", "")
		} else {
			server.Serve(listener)
		}
	}()

	transp := &http.Transport{
		DialContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
			return cnc, nil
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	scheme := "ws"
	if usetls {
		scheme = "wss"
	}

	cws, _, err := newDialer(transp).DialContext(context.Background(), scheme+"://localhost/ws", nil)
	listener.Close()
	if err != nil {
		panic(fmt.Errorf("failed to dial: %w", err))
	}
	<-hdone
	server.Close()
	<-sdone
	listener.Close()

	if servErr != nil {
		panic(fmt.Errorf("failed to upgrade: %w", servErr))
	}

	msg := [1]byte{'a'}
	_ = cws.WriteMessage(wszero.BinaryMessage, msg[:])
	_, _, err = sws.ReadMessage()
	if err != nil {
		panic(fmt.Errorf("failed to read first server message: %w", err))
	}

	_ = sws.WriteMessage(wszero.BinaryMessage, msg[:])
	_, _, err = cws.ReadMessage()
	if err != nil {
		panic(fmt.Errorf("failed to read first first client message: %w", err))
	}

	return cws, sws
}

func dup[T any](x *T) *T {
	if x == nil {
		return new(T)
	} else {
		dup := *x
		return &dup
	}
}

func newDialer(transp *http.Transport) dialer[*wszero.Conn] {
	dialer := dup(wszero.DefaultDialer)
	dialer.Client = dup(dialer.Client)
	dialer.Client.Transport = transp
	return dialer
}

func newWebsocketDialer(transp *http.Transport) dialer[*websocket.Conn] {
	dialer := dup(websocket.DefaultDialer)
	dialer.NetDialContext = transp.DialContext
	dialer.TLSClientConfig = transp.TLSClientConfig
	dialer.WriteBufferPool = &sync.Pool{}
	return dialer
}

func newGobwasDialer(transp *http.Transport) dialer[*GobwasConn] {
	dialer := dup(GobwasDefaultDialer)
	dialer.NetDial = transp.DialContext
	dialer.TLSConfig = transp.TLSClientConfig
	return dialer
}

func newNhooyrDialer(transp *http.Transport) dialer[*NhooyrConn] {
	dialer := dup(NhooyrDefaultDialer)
	dialer.HTTPClient = dup(dialer.HTTPClient)
	dialer.HTTPClient.Transport = transp
	return dialer
}

func genDialer[T wsconn](newDialer func(*http.Transport) dialer[T]) func(*http.Transport) dialer[wsconn] {
	return func(t *http.Transport) dialer[wsconn] {
		dialer := newDialer(t)
		return dialerFunc(func(ctx context.Context, s string, h http.Header) (wsconn, *http.Response, error) {
			return dialer.DialContext(ctx, s, h)
		})
	}
}

func genUpgrader[T wsconn](upgrader upgrader[T]) upgrader[wsconn] {
	return upgraderFunc(func(w http.ResponseWriter, r *http.Request, h http.Header) (wsconn, error) {
		return upgrader.Upgrade(w, r, h)
	})
}

type handshake[C, S wsconn] struct {
	s       string
	d       func(*http.Transport) dialer[C]
	ctype   wsconn
	u       upgrader[S]
	stype   wsconn
	prepare func(wsconn, wsconn)
}

var wsType wsconn = (*wszero.Conn)(nil)
var websocketType wsconn = (*websocket.Conn)(nil)
var gobwasType wsconn = (*GobwasConn)(nil)
var nhooyrType wsconn = (*NhooyrConn)(nil)

var websocketUpgrader = &websocket.Upgrader{
	WriteBufferPool: &sync.Pool{},
}

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

type runner[T any] interface {
	Run(string, func(T)) bool
}

type variant struct {
	name    string
	prepare func(wsconn, wsconn)
}

func bufPoolVariants(c, s wsconn) (vs []variant) {
	_, cok := c.(*wszero.Conn)
	_, sok := s.(*wszero.Conn)
	if !cok && !sok {
		return nil
	}

	bufC := func(c wsconn, _ wsconn) { c.(*wszero.Conn).SetBufferPool(wszero.NewBufferPool()) }
	bufS := func(_ wsconn, s wsconn) { s.(*wszero.Conn).SetBufferPool(wszero.NewBufferPool()) }
	unbufC := func(c wsconn, _ wsconn) { c.(*wszero.Conn).SetBufferPool(nil) }
	unbufS := func(_ wsconn, s wsconn) { s.(*wszero.Conn).SetBufferPool(nil) }

	if cok {
		vs = append(vs, variant{"c-nopool", unbufC}, variant{"c-pool", bufC})
	} else if sok {
		vs = append(vs, variant{"s-nopool", unbufS}, variant{"s-pool", bufS})
	}

	if cok && sok {
		for _, v := range vs {
			vs = append(vs,
				variant{v.name + ";s-nopool", func(c, s wsconn) {
					v.prepare(c, s)
					unbufS(c, s)
				}},
				variant{v.name + ";s-pool", func(c, s wsconn) {
					v.prepare(c, s)
					bufS(c, s)
				}})
		}
	}

	return
}

func foreachHandshakes[C, S wsconn, T runner[T]](t T, format string, hss []handshake[C, S], variants func(c, s wsconn) []variant, f func(T, handshake[C, S])) {
	for _, hs := range hss {
		name := hs.s
		if format != "" {
			name = fmt.Sprintf(format, name)
		}
		t.Run(name, func(t T) {
			vs := []variant(nil)
			if variants != nil {
				vs = variants(hs.ctype, hs.stype)
			}
			if len(vs) > 0 {
				for _, v := range vs {
					t.Run(v.name, func(t T) {
						hs1 := hs
						hs1.prepare = v.prepare
						f(t, hs1)
					})
				}
			} else {
				hs1 := hs
				hs1.prepare = func(s, c wsconn) {}
				f(t, hs1)
			}
		})
	}
}

func putbuf(c wsconn, d []byte) {
	if c, _ := c.(*wszero.Conn); c != nil {
		if bp := c.BufferPool(); bp != nil {
			bp.PutBuffer(d)
		}
	}
}

func writeMessageString(c wsconn, mt int, s string) error {
	if c, _ := c.(*wszero.Conn); c != nil {
		return c.WriteMessageString(mt, s)
	}
	return c.WriteMessage(mt, []byte(s))
}

func writeMessageBuffers(c wsconn, mt int, bs *net.Buffers) error {
	if c, _ := c.(*wszero.Conn); c != nil {
		_, err := c.WriteMessageBuffers(mt, bs)
		return err
	}
	bufLen := 0
	for _, b := range *bs {
		bufLen += len(b)
	}
	buf := make([]byte, 0, bufLen)
	for _, b := range *bs {
		buf = append(buf, b...)
	}
	return c.WriteMessage(mt, buf)
}

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
					closeErr := &websocket.CloseError{}
					a.True(errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.As(err, &closeErr))
					break
				}
				putbuf(s, d)
			}
		}()

		pongData := []byte(nil)
		switch cc := c.(type) {
		case *wszero.Conn:
			cc.SetPongHandler(func(c *wszero.Conn, b []byte) error {
				pongData = make([]byte, len(b))
				copy(pongData, b)
				return c.Close()
			})
		case *websocket.Conn:
			cc.SetPongHandler(func(appData string) error {
				pongData = []byte(appData)
				return cc.Close()
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

		expectedError := error(nil)
		closeCode := -1
		switch sc := s.(type) {
		case *wszero.Conn:
			sc.SetCloseHandler(func(c *wszero.Conn, code int, text string) {
				closeCode = code
			})
			expectedError = &wszero.CloseError{}
		case *websocket.Conn:
			sc.SetCloseHandler(func(code int, text string) error {
				closeCode = code
				return nil
			})
			expectedError = &websocket.CloseError{}
		}

		err := s.WriteMessage(wszero.BinaryMessage, make([]byte, 1025))
		a.NoError(err)

		c.SetReadLimit(1024)
		_, d, err := c.ReadMessage()
		a.True(strings.Contains(err.Error(), "read limit exceeded"))
		putbuf(c, d)

		_, d, err = s.ReadMessage()
		a.ErrorAs(err, &expectedError)
		a.Equal(wszero.CloseMessageTooBig, closeCode)
		putbuf(s, d)

		err = c.WriteMessage(wszero.BinaryMessage, nil)
		a.True(errors.Is(err, net.ErrClosed) || errors.Is(err, websocket.ErrCloseSent))

		_, d, err = c.ReadMessage()
		a.True(errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "read limit exceeded"))
		putbuf(c, d)
	})
}

func anyWs(c, s wsconn) (a *wszero.Conn, b wsconn) {
	if c, _ := c.(*wszero.Conn); c != nil {
		return c, s
	}
	return s.(*wszero.Conn), c
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

var useTLS = false
var testCert, _ = generateCertificate()

func generateCertificate() (tls.Certificate, error) {

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to generate private key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Example Org"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour), // Valid for 1 year
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to create certificate: %v", err)
	}

	cert := tls.Certificate{
		Certificate: [][]byte{derBytes},
		PrivateKey:  privateKey,
	}

	return cert, nil
}
