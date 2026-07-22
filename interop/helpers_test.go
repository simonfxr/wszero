package interop_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/simonfxr/wszero"
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

func compressedWsPair(co, so wszero.ConnOpts) (c *wszero.Conn, s *wszero.Conn) {
	co.EnableCompression = true
	so.EnableCompression = true
	cnc, snc := connPair()
	c = co.NewCompressedConn(cnc, true)
	s = so.NewCompressedConn(snc, false)
	return c, s
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

func anyWs(c, s wsconn) (a *wszero.Conn, b wsconn) {
	if c, _ := c.(*wszero.Conn); c != nil {
		return c, s
	}
	return s.(*wszero.Conn), c
}

var wsType wsconn = (*wszero.Conn)(nil)

var useTLS = os.Getenv("WSZERO_TEST_TLS") != ""
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
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
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
