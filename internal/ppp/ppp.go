// Package ppp implements the minimal PPP (RFC 1661) state machine needed to
// bring up a FortiGate SSL VPN tunnel: LCP (MRU, magic number, echo
// keepalives) and IPCP (RFC 1332/1877: IPv4 address and DNS servers).
//
// Frames are carried over TLS without HDLC byte stuffing; we always send the
// address/control field (ff 03) and a 2-byte protocol field, and accept
// compressed forms on receive.
package ppp

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"time"
)

// PPP protocol numbers.
const (
	ProtoIPv4  = 0x0021
	ProtoIPv6  = 0x0057
	ProtoLCP   = 0xc021
	ProtoIPCP  = 0x8021
	ProtoIPV6C = 0x8057
)

// Control packet codes.
const (
	confReq   = 1
	confAck   = 2
	confNak   = 3
	confRej   = 4
	termReq   = 5
	termAck   = 6
	codeRej   = 7
	protoRej  = 8
	echoReq   = 9
	echoRep   = 10
	discardRq = 11
)

// LCP option types.
const (
	lcpMRU   = 1
	lcpACCM  = 2
	lcpAuth  = 3
	lcpMagic = 5
	lcpPFC   = 7
	lcpACFC  = 8
)

// IPCP option types.
const (
	ipcpAddr = 3
	ipcpDNS1 = 129
	ipcpDNS2 = 131
)

// Writer sends one PPP packet (protocol + information) over the transport.
type Writer interface {
	WritePPP(proto uint16, info []byte) error
}

// Result of a completed negotiation.
type Result struct {
	LocalIP netip.Addr
	PeerIP  netip.Addr
	DNS     []netip.Addr
	MRU     int
}

// ErrTerminated is returned when the peer terminates the link.
var ErrTerminated = errors.New("el gateway cerró el enlace PPP")

// Conn holds the PPP state for one tunnel.
type Conn struct {
	w   Writer
	log func(format string, args ...any)

	mu    sync.Mutex
	id    uint8
	magic uint32
	mru   uint16

	lcpReqID        uint8
	lcpRejected     map[byte]bool
	lcpOursAcked    bool
	lcpPeerAcked    bool
	ipcpStarted     bool
	ipcpReqID       uint8
	ipcpOursAcked   bool
	ipcpPeerAcked   bool
	ipcpRejected    map[byte]bool
	localIP         [4]byte
	dns1, dns2      [4]byte
	peerIP          [4]byte
	upCh            chan Result
	upDone          chan struct{}
	upOnce          sync.Once
	lastRx          time.Time
	echoOutstanding int
}

// New creates a PPP connection that writes control packets with w.
func New(w Writer, mru int, logf func(string, ...any)) *Conn {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if mru <= 0 {
		mru = 1354
	}
	var m [4]byte
	rand.Read(m[:])
	return &Conn{
		w:            w,
		log:          logf,
		magic:        binary.BigEndian.Uint32(m[:]) | 1,
		mru:          uint16(mru),
		lcpRejected:  map[byte]bool{},
		ipcpRejected: map[byte]bool{},
		upCh:         make(chan Result, 1),
		upDone:       make(chan struct{}),
		lastRx:       time.Now(),
	}
}

// Up is signalled once LCP and IPCP are open.
func (c *Conn) Up() <-chan Result { return c.upCh }

// Start sends the initial LCP Configure-Request and keeps retransmitting
// pending requests until negotiation completes or stop is closed.
func (c *Conn) Start(stop <-chan struct{}) {
	c.mu.Lock()
	c.sendLCPReqLocked()
	c.mu.Unlock()
	go func() {
		t := time.NewTicker(3 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-c.upDone:
				return
			case <-t.C:
				c.mu.Lock()
				if !c.lcpOursAcked {
					c.sendLCPReqLocked()
				} else if c.ipcpStarted && !c.ipcpOursAcked {
					c.sendIPCPReqLocked()
				}
				c.mu.Unlock()
			}
		}
	}()
}

func (c *Conn) nextID() uint8 { c.id++; return c.id }

func (c *Conn) sendCtl(proto uint16, code, id byte, data []byte) error {
	pkt := make([]byte, 4+len(data))
	pkt[0], pkt[1] = code, id
	binary.BigEndian.PutUint16(pkt[2:], uint16(len(pkt)))
	copy(pkt[4:], data)
	return c.w.WritePPP(proto, pkt)
}

func (c *Conn) sendLCPReqLocked() {
	var opts []byte
	if !c.lcpRejected[lcpMRU] {
		opts = append(opts, lcpMRU, 4, byte(c.mru>>8), byte(c.mru))
	}
	if !c.lcpRejected[lcpMagic] {
		opts = append(opts, lcpMagic, 6)
		opts = binary.BigEndian.AppendUint32(opts, c.magic)
	}
	c.lcpReqID = c.nextID()
	c.log("PPP: LCP Configure-Request id=%d mru=%d", c.lcpReqID, c.mru)
	c.sendCtl(ProtoLCP, confReq, c.lcpReqID, opts)
}

func (c *Conn) sendIPCPReqLocked() {
	opts := []byte{ipcpAddr, 6}
	opts = append(opts, c.localIP[:]...)
	if !c.ipcpRejected[ipcpDNS1] {
		opts = append(opts, ipcpDNS1, 6)
		opts = append(opts, c.dns1[:]...)
	}
	if !c.ipcpRejected[ipcpDNS2] {
		opts = append(opts, ipcpDNS2, 6)
		opts = append(opts, c.dns2[:]...)
	}
	c.ipcpReqID = c.nextID()
	c.log("PPP: IPCP Configure-Request id=%d", c.ipcpReqID)
	c.sendCtl(ProtoIPCP, confReq, c.ipcpReqID, opts)
}

type option struct {
	typ  byte
	data []byte
	raw  []byte
}

func parseOptions(b []byte) ([]option, error) {
	var out []option
	for len(b) > 0 {
		if len(b) < 2 || int(b[1]) < 2 || int(b[1]) > len(b) {
			return nil, errors.New("opción PPP malformada")
		}
		l := int(b[1])
		out = append(out, option{typ: b[0], data: b[2:l], raw: b[:l]})
		b = b[l:]
	}
	return out, nil
}

// Decode splits a raw PPP frame into protocol and information, accepting
// address/control-field and protocol-field compression.
func Decode(frame []byte) (uint16, []byte, error) {
	if len(frame) >= 2 && frame[0] == 0xff && frame[1] == 0x03 {
		frame = frame[2:]
	}
	if len(frame) < 1 {
		return 0, nil, errors.New("trama PPP vacía")
	}
	if frame[0]&1 == 1 { // compressed 1-byte protocol
		return uint16(frame[0]), frame[1:], nil
	}
	if len(frame) < 2 {
		return 0, nil, errors.New("trama PPP corta")
	}
	return binary.BigEndian.Uint16(frame), frame[2:], nil
}

// HandleControl processes a non-data PPP packet.
func (c *Conn) HandleControl(proto uint16, info []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastRx = time.Now()
	c.echoOutstanding = 0
	switch proto {
	case ProtoLCP:
		return c.handleLCP(info)
	case ProtoIPCP:
		return c.handleIPCP(info)
	default:
		// Protocol-Reject anything else (IPv6CP, CCP, ...) once LCP is open.
		if c.lcpOursAcked && c.lcpPeerAcked {
			data := make([]byte, 2, 2+len(info))
			binary.BigEndian.PutUint16(data, proto)
			data = append(data, info...)
			if len(data) > int(c.mru)-4 {
				data = data[:int(c.mru)-4]
			}
			c.log("PPP: Protocol-Reject 0x%04x", proto)
			c.sendCtl(ProtoLCP, protoRej, c.nextID(), data)
		}
		return nil
	}
}

// NoteRx records that traffic was received (used by keepalive logic).
func (c *Conn) NoteRx() {
	c.mu.Lock()
	c.lastRx = time.Now()
	c.echoOutstanding = 0
	c.mu.Unlock()
}

// Keepalive sends an LCP Echo-Request when the link has been idle and
// returns an error when too many have gone unanswered.
func (c *Conn) Keepalive(idle time.Duration, maxMissed int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.lastRx) < idle {
		return nil
	}
	if c.echoOutstanding >= maxMissed {
		return errors.New("el gateway dejó de responder (keepalive)")
	}
	c.echoOutstanding++
	data := binary.BigEndian.AppendUint32(nil, c.magic)
	return c.sendCtl(ProtoLCP, echoReq, c.nextID(), data)
}

// Terminate politely closes the link.
func (c *Conn) Terminate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sendCtl(ProtoLCP, termReq, c.nextID(), []byte("bye"))
}

func header(info []byte) (code, id byte, data []byte, err error) {
	if len(info) < 4 {
		return 0, 0, nil, errors.New("paquete de control corto")
	}
	l := int(binary.BigEndian.Uint16(info[2:]))
	if l < 4 || l > len(info) {
		return 0, 0, nil, errors.New("longitud de paquete de control inválida")
	}
	return info[0], info[1], info[4:l], nil
}

func (c *Conn) handleLCP(info []byte) error {
	code, id, data, err := header(info)
	if err != nil {
		return nil
	}
	switch code {
	case confReq:
		opts, err := parseOptions(data)
		if err != nil {
			return nil
		}
		var rej []byte
		for _, o := range opts {
			switch o.typ {
			case lcpMRU, lcpACCM, lcpMagic, lcpPFC, lcpACFC:
			default:
				rej = append(rej, o.raw...)
			}
		}
		if len(rej) > 0 {
			c.log("PPP: LCP Configure-Reject de opciones del gateway")
			c.sendCtl(ProtoLCP, confRej, id, rej)
			return nil
		}
		c.sendCtl(ProtoLCP, confAck, id, data)
		c.lcpPeerAcked = true
		c.maybeStartIPCPLocked()
	case confAck:
		if id == c.lcpReqID {
			c.lcpOursAcked = true
			c.log("PPP: LCP aceptado por el gateway")
			c.maybeStartIPCPLocked()
		}
	case confNak:
		if opts, err := parseOptions(data); err == nil {
			for _, o := range opts {
				if o.typ == lcpMRU && len(o.data) == 2 {
					c.mru = binary.BigEndian.Uint16(o.data)
				}
				if o.typ == lcpMagic {
					var m [4]byte
					rand.Read(m[:])
					c.magic = binary.BigEndian.Uint32(m[:]) | 1
				}
			}
		}
		c.sendLCPReqLocked()
	case confRej:
		if opts, err := parseOptions(data); err == nil {
			for _, o := range opts {
				c.lcpRejected[o.typ] = true
			}
		}
		c.sendLCPReqLocked()
	case termReq:
		c.sendCtl(ProtoLCP, termAck, id, nil)
		return ErrTerminated
	case termAck:
		return ErrTerminated
	case echoReq:
		reply := binary.BigEndian.AppendUint32(nil, c.magic)
		if len(data) > 4 {
			reply = append(reply, data[4:]...)
		}
		c.sendCtl(ProtoLCP, echoRep, id, reply)
	case echoRep, discardRq:
	case codeRej, protoRej:
		c.log("PPP: el gateway rechazó un paquete (code %d)", code)
	default:
		c.sendCtl(ProtoLCP, codeRej, c.nextID(), info)
	}
	return nil
}

func (c *Conn) maybeStartIPCPLocked() {
	if c.lcpOursAcked && c.lcpPeerAcked && !c.ipcpStarted {
		c.ipcpStarted = true
		c.sendIPCPReqLocked()
	}
}

func (c *Conn) handleIPCP(info []byte) error {
	code, id, data, err := header(info)
	if err != nil {
		return nil
	}
	switch code {
	case confReq:
		opts, err := parseOptions(data)
		if err != nil {
			return nil
		}
		var rej []byte
		for _, o := range opts {
			if o.typ == ipcpAddr && len(o.data) == 4 {
				copy(c.peerIP[:], o.data)
			} else {
				rej = append(rej, o.raw...)
			}
		}
		if len(rej) > 0 {
			c.sendCtl(ProtoIPCP, confRej, id, rej)
			return nil
		}
		c.sendCtl(ProtoIPCP, confAck, id, data)
		c.ipcpPeerAcked = true
		c.maybeUpLocked()
	case confAck:
		if id == c.ipcpReqID {
			c.ipcpOursAcked = true
			c.maybeUpLocked()
		}
	case confNak:
		if opts, err := parseOptions(data); err == nil {
			for _, o := range opts {
				if len(o.data) != 4 {
					continue
				}
				switch o.typ {
				case ipcpAddr:
					copy(c.localIP[:], o.data)
				case ipcpDNS1:
					copy(c.dns1[:], o.data)
				case ipcpDNS2:
					copy(c.dns2[:], o.data)
				}
			}
		}
		c.sendIPCPReqLocked()
	case confRej:
		if opts, err := parseOptions(data); err == nil {
			for _, o := range opts {
				if o.typ == ipcpAddr {
					return errors.New("el gateway rechazó la negociación de dirección IP")
				}
				c.ipcpRejected[o.typ] = true
			}
		}
		c.sendIPCPReqLocked()
	case termReq:
		c.sendCtl(ProtoIPCP, termAck, id, nil)
		return ErrTerminated
	}
	return nil
}

func (c *Conn) maybeUpLocked() {
	if !(c.ipcpOursAcked && c.ipcpPeerAcked) {
		return
	}
	c.upOnce.Do(func() {
		r := Result{LocalIP: netip.AddrFrom4(c.localIP), PeerIP: netip.AddrFrom4(c.peerIP), MRU: int(c.mru)}
		for _, d := range [][4]byte{c.dns1, c.dns2} {
			if a := netip.AddrFrom4(d); !a.IsUnspecified() {
				r.DNS = append(r.DNS, a)
			}
		}
		c.log("PPP: enlace establecido, IP local %s, DNS %v", r.LocalIP, r.DNS)
		c.upCh <- r
		close(c.upDone)
	})
}

// String is for debugging.
func (r Result) String() string {
	return fmt.Sprintf("ip=%s peer=%s dns=%v mru=%d", r.LocalIP, r.PeerIP, r.DNS, r.MRU)
}
