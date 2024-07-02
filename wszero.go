// package wszero provides a WebSocket implementation.
package wszero

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unsafe"
)

const (
	// TextMessage represents a text message type.
	TextMessage int = 0x1
	// BinaryMessage represents a binary message type.
	BinaryMessage int = 0x2
	// CloseMessage represents a close message type.
	CloseMessage int = 0x8
	// PingMessage represents a ping message type.
	PingMessage int = 0x9
	// PongMessage represents a pong message type.
	PongMessage int = 0xA
)

const (
	CloseNormalClosure           = 1000
	CloseGoingAway               = 1001
	CloseProtocolError           = 1002
	CloseUnsupportedData         = 1003
	CloseNoStatusReceived        = 1005
	CloseAbnormalClosure         = 1006
	CloseInvalidFramePayloadData = 1007
	ClosePolicyViolation         = 1008
	CloseMessageTooBig           = 1009
	CloseMandatoryExtension      = 1010
	CloseInternalServerErr       = 1011
	CloseServiceRestart          = 1012
	CloseTryAgainLater           = 1013
	CloseTLSHandshake            = 1015
)

const (
	maxCtlFrameSz     = 14 + 125
	MinReadBufferSize = maxCtlFrameSz
)

var (
	// ErrBadHandshake is returned when the server response to opening handshake is invalid.
	ErrBadHandshake = errors.New("ws: bad handshake")

	// ErrProtocol is returned when a protocol error occurs.
	ErrProtocol = errors.New("ws: protocol violation")

	// ErrReadLimit is returned when the read limit is exceeded.
	ErrReadLimit = errors.New("ws: read limit exceeded")
)

var globalPool = NewBufferPool()

// ConnOpts represents options for creating a WebSocket connection.
type ConnOpts struct {
	ReadBufferSize  int        // The size of the read buffer.
	WriteBufferSize int        // The size of the write buffer.
	ReadLimit       int        // The maximum size of a message read.
	BufferPool      BufferPool // The buffer pool for reusing buffers, if nil GlobalBufferPool() is used
}

// Conn represents a WebSocket connection.
type Conn struct {
	r            bufReader
	rlim         int
	bp           BufferPool
	wmu          sync.Mutex
	writevb      net.Buffers
	writevbs     [2][]byte // for net.Buffers
	wb           []byte
	isClient     byte // 0 or 1
	writev       bool
	closeHandler func(*Conn, int, string)
	pingHandler  func(*Conn, []byte) error
	pongHandler  func(*Conn, []byte) error
}

// Dialer contains options for connecting to a WebSocket server.
type Dialer struct {
	*http.Client // The HTTP client to use for the connection.
	ConnOpts     // The connection options.
}

// Upgrader specifies parameters for upgrading an HTTP connection to a WebSocket connection.
type Upgrader struct {
	ConnOpts // The connection options.
}

// CloseError represents a close message.
type CloseError struct {
	Code int    // The status code.
	Text string // The reason for closing.
}

// BufferPool is an interface for getting and putting buffers.
type BufferPool interface {
	// GetBuffer tries to allocate a buffer with at least size bytes in length or returns
	// nil if no such buffer is available.
	GetBuffer(size int) []byte
	// PutBuffer puts a buffer with all its capacity into the pool. A given
	// buffer may be returned later on from GetBuffer. If the buffer was
	// successfully inserted true is returned and any other references to the
	// buffer must be dropped.
	PutBuffer([]byte) (ok bool)
}

// A trivial BufferPool implementation that does nothing. It can be used to
// disable buffer pooling.
type EmptyBufferPool struct{}

func (e *CloseError) Error() string {
	return fmt.Sprintf("CloseError(%d) %s", e.Code, e.Text)
}

func def(v, def, low int) int {
	if v <= 0 {
		v = def
	}
	return max(v, low)
}

func (o ConnOpts) readBufSz() int { return def(o.ReadBufferSize, 4096, maxCtlFrameSz) }

func (o ConnOpts) writeBufSz() int { return def(o.WriteBufferSize, maxCtlFrameSz, maxCtlFrameSz) }

func (o ConnOpts) readLimit() int { return def(o.ReadLimit, 16<<20, maxCtlFrameSz) }

// NewConn wraps a net.Conn in a WebSocket connection without any handshakes.
// Useful to add framing to an existing connection.
func (o ConnOpts) NewConn(nc net.Conn, isClient bool) *Conn {
	c, _ := o.newConn(nc, nil, isClient)
	return c
}

func (o ConnOpts) newConn(nc net.Conn, br *bufio.Reader, isClient bool) (*Conn, error) {
	_ = nc.SetDeadline(time.Time{})
	wv := false
	switch nc.(type) {
	case (*net.IPConn), (*net.TCPConn), (*net.UDPConn), (*net.UnixConn):
		wv = true
	}
	rbuf := o.readBufSz()
	r := bufReader{}
	r.reset(nc, make([]byte, rbuf))
	if br != nil && br.Buffered() > 0 {
		p, _ := br.Peek(br.Buffered())
		if len(p) > rbuf {
			return nil, fmt.Errorf("bufio.Reader data exceeds configured reader buffer size")
		} else {
			r.buf = r.buf[:len(p)]
			copy(r.buf, p)
		}
	}
	wb := make([]byte, o.writeBufSz())
	rlim, bp, isClientB := o.readLimit(), globalPool, byte(0)
	if isClient {
		isClientB = 1
	}
	if o.BufferPool != nil {
		bp = o.BufferPool
	}
	if _, ok := bp.(EmptyBufferPool); ok {
		bp = nil
	}
	c := &Conn{r: r, writev: wv, rlim: rlim, isClient: isClientB, wb: wb, bp: bp}
	c.closeHandler, c.pingHandler, c.pongHandler = handleClose, handlePing, handlePong
	return c, nil
}

// LocalAddr returns the local network address.
func (c *Conn) LocalAddr() net.Addr { return c.r.nc.LocalAddr() }

// RemoteAddr returns the remote network address.
func (c *Conn) RemoteAddr() net.Addr { return c.r.nc.RemoteAddr() }

// SetDeadline sets the read and write deadlines associated with the connection.
func (c *Conn) SetDeadline(t time.Time) error { return c.r.nc.SetDeadline(t) }

// SetReadDeadline sets the deadline for future Read calls and any currently-blocked Read call.
func (c *Conn) SetReadDeadline(t time.Time) error { return c.r.nc.SetReadDeadline(t) }

// SetWriteDeadline sets the deadline for future Write calls and any currently-blocked Write call.
func (c *Conn) SetWriteDeadline(t time.Time) error { return c.r.nc.SetWriteDeadline(t) }

// NetConn returns the underlying network connection.
func (c *Conn) NetConn() net.Conn { return c.r.nc }

// Close closes the connection.
func (c *Conn) Close() error { return c.r.nc.Close() }

// SetReadLimit sets the maximum size for a message read from the peer.
func (c *Conn) SetReadLimit(limit int64) {
	c.rlim = def(int(min(int64(math.MaxInt), limit)), 4096, maxCtlFrameSz)
}

// SetBufferPool sets or unsets the buffer pool for the connection and returns
// the previously set BufferPool. Always returns a non nil buffer pool.
func (c *Conn) SetBufferPool(pool BufferPool) (old BufferPool) {
	if _, ok := pool.(EmptyBufferPool); ok {
		pool = nil
	}
	old, c.bp = c.bp, pool
	if old == nil {
		old = EmptyBufferPool{}
	}
	return
}

// BufferPool returns the current buffer pool for the connection. Always returns
// a non nil buffer pool.
func (c *Conn) BufferPool() (cur BufferPool) {
	if cur = c.bp; cur == nil {
		cur = EmptyBufferPool{}
	}
	return
}

// CloseHandler returns the current close handler.
func (c *Conn) CloseHandler() func(c *Conn, code int, text string) {
	return c.closeHandler
}

// SetCloseHandler sets the handler for close messages received from the peer.
func (c *Conn) SetCloseHandler(h func(c *Conn, code int, text string)) {
	if h == nil {
		h = handleClose
	}
	c.closeHandler = h
}

// PingHandler returns the current ping handler.
func (c *Conn) PingHandler() func(*Conn, []byte) error {
	return c.pingHandler
}

// SetPingHandler sets the handler for ping messages received from the peer.
func (c *Conn) SetPingHandler(h func(*Conn, []byte) error) {
	if h == nil {
		h = handlePing
	}
	c.pingHandler = h
}

// PongHandler returns the current pong handler.
func (c *Conn) PongHandler() func(*Conn, []byte) error {
	return c.pongHandler
}

// SetPongHandler sets the handler for pong messages received from the peer.
func (c *Conn) SetPongHandler(h func(*Conn, []byte) error) {
	if h == nil {
		h = handlePong
	}
	c.pongHandler = h
}

// IsClientSide indicates wether the WebSocket connection is client side or
// server side.
func (c *Conn) IsClientSide() bool { return c.isClient != 0 }

// Buffered returns the amount of bytes buffered for reading.
func (c *Conn) Buffered() int { return c.r.buffered() }

// SetReadBuffer changes the read buffer for reading. The given buffer must be
// of length at least MinReadBufferSize and must be large enough to hold all
// currently buffered data.
func (c *Conn) SetReadBuffer(buf []byte) (ok bool) {
	if len(buf) < MinReadBufferSize {
		return false
	}
	return c.r.setBuffer(buf[:len(buf):len(buf)])
}

func (c *Conn) readN(n int) ([]byte, error) {
	bs, err := c.r.consume(n)
	return bs, err
}

func (c *Conn) abortRead(err error, recycle []byte) error {
	if bp := c.bp; bp != nil && cap(recycle) > 0 {
		bp.PutBuffer(recycle)
	}
	closeCode := -1
	switch err {
	case ErrProtocol:
		closeCode = CloseProtocolError
	case ErrReadLimit:
		closeCode = CloseMessageTooBig
	default:
	}
	if closeCode >= 0 {
		msg := [2]byte{}
		binary.BigEndian.PutUint16(msg[:], uint16(closeCode))
		_ = c.WriteMessage(CloseMessage, msg[:])
	}
	err1 := c.r.nc.Close()
	c.r.reset(c.r.nc, c.r.buf)
	c.r.err = net.ErrClosed
	if err1 != nil && errors.Is(err1, net.ErrClosed) {
		return err
	}
	return fmt.Errorf("%w, closing websocket: %w", err, err1)
}

// ReadMessage reads a message from the connection. If a buffer pool is
// configured, ReadMessage may return buffers obtained from the pool. After you
// are done using the returned data, you can put it back into the pool for later
// reuse.
func (c *Conn) ReadMessage() (messageType int, data []byte, err error) {
	for {
		fr := []byte(nil)
		if fr, err = c.readN(2); err != nil {
			return messageType, data, err
		}
		b1, b0 := fr[1], fr[0]
		if b0&0x70 != 0 /* unknown reserved bits set */ ||
			(messageType == 0 && b0&0xF == 0) /* first frame mustn't be a continuation frame */ ||
			(messageType != 0 && b0&0xF != 0 && b0&0x8 == 0) /* following frame must be either ctl or continuation */ {
			return 0, nil, c.abortRead(ErrProtocol, data)
		}
		mlen := uint64(b1 & 0x7F)
		n := 4 & int((b1&0x80)>>5) // 4 if mask bit is set, 0 otherwise
		if mlen >= 126 {
			n += 2 + 6&(126-int(mlen)) // add 2 or 8 bytes for the encoded msg len
		}
		if fr, err = c.readN(n); err != nil {
			return messageType, data, err
		}
		if mlen >= 126 {
			if mlen == 127 {
				mlen = binary.BigEndian.Uint64(fr[:8])
				fr = fr[8:]
			} else {
				mlen = uint64(binary.BigEndian.Uint16(fr[:2]))
				fr = fr[2:]
			}
		}
		if b0&0x8 == 0 && mlen > uint64(c.rlim-len(data)) {
			return 0, nil, c.abortRead(ErrReadLimit, data)
		} else if b0&0x8 != 0 && (mlen > 125 || b0&0x80 == 0) {
			return 0, nil, c.abortRead(ErrProtocol, data)
		}
		mask := uint32(0)
		if b1&0x80 != 0 {
			mask = binary.NativeEndian.Uint32(fr)
		}
		if sz := len(data) + int(mlen); sz > cap(data) {
			rbp := c.bp
			data1 := []byte(nil)
			if rbp != nil {
				if data1 = rbp.GetBuffer(sz); data1 != nil {
					data1 = data1[:len(data)]
				}
			}
			if data1 == nil {
				sz = max(sz, cap(data)*2)
				data1 = make([]byte, len(data), sz)
			}
			copy(data1, data)
			if len(data) > 0 && rbp != nil {
				rbp.PutBuffer(data)
			}
			data = data1
		}
		nr := 0
		p := data[len(data) : len(data)+int(mlen)]
		if len(p) <= len(c.r.buf)-c.r.r {
			n = copy(p, c.r.buf[c.r.r:len(c.r.buf)])
			c.r.r += n
			nr = n
			err = nil
		} else {
			nr, err = io.ReadFull(&c.r, p)
		}
		if mask != 0 {
			xorMask(data[len(data):len(data)+nr], mask)
		}
		if b0&0x8 != 0 {
			if err == nil {
				if err = c.handleControl(b0, data[len(data):len(data)+nr], data); err != nil {
					return messageType, data, err
				}
				continue
			}
			nr = 0
		}
		data = data[:len(data)+nr]
		if err != nil {
			return messageType, data, err
		}
		if messageType == 0 {
			messageType = int(b0 & 0xF)
			if messageType > 2 { // neither text nor binary message
				return 0, nil, c.abortRead(ErrProtocol, data)
			}
		}
		if b0&0x80 != 0 {
			return messageType, data, nil
		}
	}
}

func (c *Conn) handleControl(b0 byte, data []byte, recycle []byte) error {
	switch int(b0 ^ 0x80) {
	case CloseMessage:
		closeErr := &CloseError{
			Code: CloseAbnormalClosure,
			Text: string(data[min(2, len(data)):]),
		}
		if len(data) >= 2 {
			closeErr.Code = int(binary.BigEndian.Uint16(data[:2]))
		}
		c.closeHandler(c, closeErr.Code, closeErr.Text)
		err1 := c.r.nc.Close()
		if err1 == nil {
			err1 = io.EOF
		}
		return fmt.Errorf("%w: %w", closeErr, err1)
	case PingMessage:
		return c.pingHandler(c, data)
	case PongMessage:
		return c.pongHandler(c, data)
	default:
		// this case handles both unset fin bits and invalid message types
		return c.abortRead(ErrProtocol, recycle)
	}
}

func handleClose(c *Conn, code int, text string) {}

func handlePing(c *Conn, data []byte) error {
	return c.WriteControl(PongMessage, data, time.Time{})
}

func handlePong(*Conn, []byte) error { return nil }

func (c *Conn) prepareHeader(messageType int, fin byte, n int64, fr []byte) []byte {
	fr = fr[:16] // front load all bounds checks
	b0 := byte(messageType&0xF) | byte(fin<<7)
	b1, msklen := c.isClient<<7, int(c.isClient<<2)
	for i := range fr { // ensure we have a zero mask
		fr[i] = 0
	}
	fr[0] = b0
	hlen := 0
	if n <= 125 {
		fr[1] = b1 | byte(n)
		hlen = 2 + msklen
	} else if n <= math.MaxUint16 {
		fr[1] = b1 | 126
		binary.BigEndian.PutUint16(fr[2:4], uint16(n))
		hlen = 4 + msklen
	} else {
		fr[1] = b1 | 127
		binary.BigEndian.PutUint64(fr[2:10], uint64(n))
		hlen = 10 + msklen
	}
	return fr[:hlen&0xF] // mask to prevent superflous bounds checks
}

func (c *Conn) writeFragment(messageType int, fin byte, data []byte) (n int64, err error) {
	bp := BufferPool(nil)
	l := len(data)
	writev := true
	wb := c.wb
	if c.writev && l <= 125 {
		writev = false
	} else if !c.writev {
		if l <= len(c.wb)-14 {
			writev = false
		} else if l <= 4096-16 {
			if bp = c.bp; bp != nil {
				wb = bp.GetBuffer(l + 16)
				if wb == nil {
					bp = nil
				} else {
					writev = false
				}
			}
		}
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	fr := c.prepareHeader(messageType, fin, int64(l), wb)
	lfr := int64(len(fr))
	if writev {
		c.writevb = c.writevbs[:]
		c.writevbs[0], c.writevbs[1] = fr, data
		n, err = io.Copy(c.r.nc, &c.writevb)
	} else {
		nn, err1 := c.r.nc.Write(append(fr, data...))
		n, err = int64(nn), err1
	}
	if bp != nil {
		bp.PutBuffer(wb)
	}
	if n <= lfr {
		return 0, err
	}
	return n - lfr, err
}

// WriteMessage writes a message to the connection.
func (c *Conn) WriteMessage(messageType int, data []byte) error {
	_, err := c.writeFragment(messageType, 1, data)
	return err
}

// WriteMessageString writes a string message to the connection.
func (c *Conn) WriteMessageString(messageType int, s string) error {
	return c.WriteMessage(messageType, unsafe.Slice(unsafe.StringData(s), len(s)))
}

// WriteControl writes a control message. The deadline argument is ignored.
func (c *Conn) WriteControl(messageType int, data []byte, deadline time.Time) error {
	_, err := c.writeFragment(messageType, 1, data)
	return err
}

// WriteMessageBuffers writes a message using the provided buffers avoiding any
// intermediate buffer copies.
func (c *Conn) WriteMessageBuffers(messageType int, bs *net.Buffers) (n int64, err error) {
	return c.writeFragmentBuffers(messageType, 1, bs)
}

func (c *Conn) writeFragmentBuffers(messageType int, fin byte, bs *net.Buffers) (n int64, err error) {
	if len(*bs) == 0 {
		return c.writeFragment(messageType, fin, nil)
	}
	n = 0
	for _, b := range *bs {
		n += int64(len(b))
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	fr := c.prepareHeader(messageType, fin, n, c.wb)
	nfr := int64(len(fr))
	*bs = append(*bs, nil)
	copy((*bs)[1:], (*bs))
	(*bs)[0] = fr
	m := len(*bs)
	n, err = io.Copy(c.r.nc, bs)
	if m == len(*bs) { // remove writefr buf
		(*bs) = (*bs)[1:]
	}
	if n <= nfr {
		return 0, err
	}
	return n - nfr, err
}

// FrameWriter represents a writer for WebSocket frames.
type FrameWriter struct {
	c           *Conn
	messageType byte
	fin         byte
}

// FrameWriter returns a new FrameWriter for writing a WebSocket message of the
// given type.
func (c *Conn) FrameWriter(messageType int) (fw *FrameWriter) {
	fw = &FrameWriter{}
	fw.Reset(c, messageType)
	return fw
}

// Reset resets the writer to start a new message.
func (w *FrameWriter) Reset(c *Conn, messageType int) {
	if !(0 < messageType && messageType <= BinaryMessage) {
		panic(fmt.Errorf("invalid FrameWriter message type: %d", messageType))
	}
	w.c, w.messageType, w.fin = c, byte(messageType), 0
}

// Write writes a single frame to the WebSocket. You should call Final()
// beforehand if this frame is the last of the current message.
func (w *FrameWriter) Write(b []byte) (err error) {
	if w.fin&2 != 0 {
		return io.EOF
	}
	if len(b) > 0 {
		_, err = w.c.writeFragment(int(w.messageType), w.fin&1, b)
		w.afterWrite(err)
	}
	return err
}

// WriteString writes a single string frame to the WebSocket. You should call
// Final() beforehand if this frame is the last of the current message.
func (w *FrameWriter) WriteString(s string) error {
	return w.Write(unsafe.Slice(unsafe.StringData(s), len(s)))
}

// WriteBuffers writes a single frame using the provided buffers avoiding any
// intermediate buffer copies. You should call Final() beforehand if this frame
// is the last of the current message.
func (w *FrameWriter) WriteBuffers(bs *net.Buffers) (n int64, err error) {
	if w.fin&2 != 0 {
		return 0, io.EOF
	}
	n, err = w.c.writeFragmentBuffers(int(w.messageType), w.fin&1, bs)
	w.afterWrite(err)
	return
}

func (w *FrameWriter) afterWrite(err error) {
	w.messageType = 0
	w.fin |= w.fin << 1
	if err != nil {
		w.fin = 0xFF
	}
}

// Final marks the next frame to be written as the final frame in the message.
func (w *FrameWriter) Final() { w.fin |= 1 }

// Close completes the message by sending a final empty frame if no final frame
// was yet written and marks the writer as closed.
func (w *FrameWriter) Close() (err error) {
	if w.fin&2 == 0 { // write an empty final frame?
		_, err = w.c.writeFragment(int(w.messageType), 1, nil)
		w.afterWrite(err)
	}
	w.fin = 0xFF
	return
}

// Conn returns the underlying WebSocket connection.
func (w *FrameWriter) Conn() *Conn { return w.c }

func acceptKey(key string) string {
	if key == "" {
		return ""
	}
	hbs := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(hbs[:])
}

// Upgrade upgrades the HTTP server connection to the WebSocket protocol.
func (u *Upgrader) Upgrade(w http.ResponseWriter, r *http.Request, uh http.Header) (*Conn, error) {
	o := u.ConnOpts
	if r.Method != http.MethodGet {
		return nil, ErrBadHandshake
	}
	if r.ProtoMajor != 1 {
		return nil, ErrBadHandshake
	}
	rh := func(k string) string { return strings.ToLower(r.Header.Get(k)) }
	if rh("Connection") != "upgrade" || rh("Upgrade") != "websocket" || rh("Sec-Websocket-Version") != "13" {
		return nil, ErrBadHandshake
	}
	akey := acceptKey(r.Header.Get("Sec-Websocket-Key"))
	if akey == "" {
		return nil, ErrBadHandshake
	}
	for k, h := range uh {
		w.Header()[k] = h
	}
	w.Header().Set("Sec-Websocket-Accept", akey)
	w.Header().Set("Upgrade", "websocket")
	w.Header().Set("Connection", "upgrade")
	w.WriteHeader(http.StatusSwitchingProtocols)
	rc := http.NewResponseController(w)
	rc.Flush()
	nc, brw, err := rc.Hijack()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadHandshake, err)
	}
	if brw.Writer.Buffered() > 0 {
		nc.Close()
		return nil, errors.New("already some bytes written")
	}
	c, err := o.newConn(nc, brw.Reader, false)
	if err != nil {
		nc.Close()
		return nil, err
	}
	return c, nil
}

var defaultClient = http.Client{
	Transport: func() *http.Transport {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.ForceAttemptHTTP2 = false
		t.DisableKeepAlives = true
		t.DisableCompression = true
		return t
	}(),
}

// DefaultDialer is a dialer with default options.
var DefaultDialer = &Dialer{}

// DialContext creates a new client connection using the provided context.
func (d *Dialer) DialContext(ctx context.Context, urlStr string, rh http.Header) (*Conn, *http.Response, error) {
	client := d.Client
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil, nil, err
	}
	switch u.Scheme {
	case "ws", "":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	if len(rh) > 0 {
		r.Header = rh.Clone()
	}
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Connection", "upgrade")
	r.Header.Set("Sec-Websocket-Version", "13")
	keyBytes := [16]byte{}                                     // only first 8 bytes are random
	binary.NativeEndian.PutUint64(keyBytes[:8], rand.Uint64()) // we don't care about entropy
	key := base64.StdEncoding.EncodeToString(keyBytes[:])
	r.Header.Set("Sec-Websocket-Key", key)
	if client == nil {
		client = &defaultClient
	}
	resp, err := client.Do(r)
	if err != nil {
		return nil, nil, err
	}
	body := resp.Body
	resp.Body = io.NopCloser(bytes.NewReader(nil))
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body.Close()
		return nil, resp, ErrBadHandshake
	}
	sh := resp.Header.Get
	h := func(k string) string { return strings.ToLower(sh(k)) }
	if h("Upgrade") != "websocket" || h("Connection") != "upgrade" || sh("Sec-Websocket-Accept") != acceptKey(key) {
		body.Close()
		return nil, resp, ErrBadHandshake
	}
	nc, br := hijackResponseConn(body.(io.ReadWriteCloser))
	c, err := d.newConn(nc, br, true)
	if err != nil {
		body.Close()
		return nil, resp, err
	}
	return c, resp, nil
}

func hijackResponseConn(rwc io.ReadWriteCloser) (nc net.Conn, br *bufio.Reader) {
	v := reflect.ValueOf(rwc)
	if v.Type().Kind() != reflect.Pointer || v.Type().String() != "*http.readWriteCloserBody" {
		panic(fmt.Sprintf("cannot unwrap net.Conn from http.Response body, type=%s", v.Type()))
	}
	if rwc := v.Elem().FieldByName("ReadWriteCloser"); !rwc.IsZero() {
		nc, _ = rwc.Interface().(net.Conn)
	}
	if nc == nil {
		panic("cannot unwrap net.Conn from http.Response body")
	}
	va := v.Interface()
	t := v.Type().Elem()
	p := (*[2]unsafe.Pointer)(unsafe.Pointer(&va))[1]
	if sf, _ := t.FieldByName("br"); sf.Type == reflect.TypeOf((*bufio.Reader)(nil)) {
		br = *(**bufio.Reader)(unsafe.Add(p, sf.Offset))
	}
	return nc, br
}

// GlobalBufferPool returns the shared global buffer pool.
func GlobalBufferPool() BufferPool {
	return globalPool
}

type segBufferBool struct {
	sizes []int
	pools []sync.Pool // size seggregated
}

var defaultPoolSizes = []int{1 << 10, 1 << 11, 1 << 12, 1 << 14, 1 << 16, 1 << 18, 1 << 20}

// NewBufferPool creates a new BufferPool that caches buffers in the given size
// classes. If the list of sizes is empty, a reasonable default is used. The
// pool internally builds on top of sync.Pool.
func NewBufferPool(sizes ...int) BufferPool {
	if len(sizes) > 0 {
		for _, sz := range sizes {
			if sz <= 0 {
				panic("sizes must be positive")
			}
		}
		sizesDup := make([]int, len(sizes))
		copy(sizesDup, sizes)
		sizes = sizesDup
		sort.Slice(sizes, func(i, j int) bool {
			return sizes[i] < sizes[j]
		})
	} else {
		sizes = defaultPoolSizes
	}
	pools := make([]sync.Pool, len(sizes))
	for i, sz := range sizes {
		sz := sz
		pools[i].New = func() any { return unsafe.SliceData(make([]byte, sz)) }
	}
	return &segBufferBool{sizes: sizes, pools: pools}
}

func (s *segBufferBool) class(n int) int {
	for i, sz := range s.sizes {
		if n <= sz {
			return i
		}
	}
	return -1
}

func (s *segBufferBool) GetBuffer(n int) []byte {
	if i := s.class(n); i >= 0 && n > 0 {
		return unsafe.Slice(s.pools[i].Get().(*byte), s.sizes[i])
	}
	return nil
}

func (s *segBufferBool) PutBuffer(b []byte) bool {
	if i := s.class(cap(b)); i >= 0 && s.sizes[i] == cap(b) {
		s.pools[i].Put(unsafe.SliceData(b))
		return true
	}
	return false
}

func (EmptyBufferPool) GetBuffer(int) []byte { return nil }

func (EmptyBufferPool) PutBuffer([]byte) bool { return false }

const (
	wordSize        = int(unsafe.Sizeof(uint(0)))
	logWordSize     = 2 + wordSize/8
	unalignedAccess = runtime.GOARCH == "amd64" || runtime.GOARCH == "386" ||
		runtime.GOARCH == "ppc64" || runtime.GOARCH == "ppc64le"
)

// The following function is based on code adapted from here:
// https://raw.githubusercontent.com/gorilla/websocket/b2c246b2ec6f86b53889c79022fec8dabe0a20bb/mask.go
// Licensed under BSD-2-Clause license
func xorMask(b []byte, key uint32) {
	pos := 0
	if len(b) >= 3*wordSize {
		if !unalignedAccess {
			// align to word boundary
			if n := int(uintptr(unsafe.Pointer(unsafe.SliceData(b))) % uintptr(wordSize)); n != 0 {
				n = wordSize - n
				b1 := b[:n]
				b = b[n:]
				for i := range b1 {
					b1[i] ^= byte(key >> (((pos & 3) << 3) & 31))
					pos++
				}
			}
		}

		keyw := bits.RotateLeft(uint(uint64(key)<<32|uint64(key)), (wordSize-(pos&3))<<3)
		// words
		ws := unsafe.Slice((*uint)(unsafe.Pointer(unsafe.SliceData(b))), len(b)>>logWordSize)
		// remaining bytes
		b = b[len(ws)<<logWordSize:]

		const logLanes int = 2
		const lanes = 1 << logLanes
		if len(ws) >= lanes {
			// word tuples
			vs := unsafe.Slice((*[lanes]uint)(unsafe.Pointer(unsafe.SliceData(ws))), len(ws)>>logLanes)
			for i := range vs {
				vs[i][0] ^= keyw
				vs[i][1] ^= keyw
				vs[i][2] ^= keyw
				vs[i][3] ^= keyw
			}
			// remaining words
			ws = ws[len(vs)<<logLanes:]
		}

		for i := range ws {
			ws[i] ^= keyw
		}
	}

	// remaining bytes
	for i := range b {
		b[i] ^= byte(key >> (((pos & 3) << 3) & 31))
		pos++
	}
}

// A buffered bufReader similar to bufio.Reader but more fitting for our use case.
type bufReader struct {
	buf []byte
	r   int
	err error
	nc  net.Conn
}

func (b *bufReader) buffered() int { return len(b.buf) - b.r }

func (b *bufReader) reset(rd net.Conn, buf []byte) {
	b.nc, b.r, b.buf = rd, 0, buf[:0]
}

func (b *bufReader) setBuffer(buf []byte) (ok bool) {
	n := b.buffered()
	if cap(buf) < n {
		return false
	}

	copy(buf[:n], b.buf[b.r:])
	b.buf = buf[:n]
	b.r = 0
	return true
}

func (b *bufReader) consume(n int) (p []byte, err error) {
	if n <= len(b.buf)-b.r {
		p = b.buf[b.r : b.r+n]
		b.r += n
		return p, nil
	}
	return b.consumeSlow(n)
}

func (b *bufReader) consumeSlow(n int) (p []byte, err error) {
	for len(b.buf)-b.r < n && b.err == nil {
		b.fill(n)
	}

	if avail := len(b.buf) - b.r; avail < n {
		n = avail
		err = b.err
		if err == nil {
			err = bufio.ErrBufferFull
		}
	}
	p = b.buf[b.r : b.r+n]
	b.r += n
	return p, err
}

func (b *bufReader) fill(atleast int) {
	if b.r > 0 {
		copy(b.buf, b.buf[b.r:])
		b.buf = b.buf[:len(b.buf)-b.r]
		b.r = 0
	}
	atleast -= len(b.buf)

	for i := 0; i < 100; i++ {
		n, err := b.nc.Read(b.buf[len(b.buf):cap(b.buf)])
		b.buf = b.buf[:len(b.buf)+n]
		atleast -= n
		if err != nil {
			b.err = err
			return
		}
		if n > 0 {
			i = 0
			if atleast <= 0 {
				return
			}
		}
	}
	b.err = io.ErrNoProgress
}

func (b *bufReader) Read(p []byte) (n int, err error) {
	if b.r < len(b.buf) {
		n = copy(p, b.buf[b.r:])
		b.r += n
		return n, nil
	}
	return b.readSlow(p)
}

func (b *bufReader) readSlow(p []byte) (n int, err error) {
	if n = len(p); n == 0 {
		return 0, b.err
	}
	if b.r == len(b.buf) {
		if b.err != nil {
			return 0, b.err
		}
		if len(p) >= cap(b.buf) {
			n, b.err = b.nc.Read(p)
			return n, b.err
		}
		b.r = 0
		b.buf = b.buf[:0]
		n, b.err = b.nc.Read(b.buf[:cap(b.buf)])
		if n == 0 {
			return 0, b.err
		}
		b.buf = b.buf[:n]
	}
	n = copy(p, b.buf[b.r:len(b.buf)])
	b.r += n
	return n, nil
}
