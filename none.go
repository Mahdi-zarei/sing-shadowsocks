package shadowsocks

import (
	"context"
	"io"
	"net"
	"net/netip"
	"sync"

	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/udpnat"
)

const MethodNone = "none"

type NoneMethod struct{}

func NewNone() Method {
	return &NoneMethod{}
}

func (m *NoneMethod) Name() string {
	return MethodNone
}

func (m *NoneMethod) KeyLength() int {
	return 0
}

func (m *NoneMethod) DialConn(conn net.Conn, destination M.Socksaddr) (net.Conn, error) {
	shadowsocksConn := &noneConn{
		Conn:        conn,
		handshake:   true,
		destination: destination,
	}
	return shadowsocksConn, shadowsocksConn.clientHandshake()
}

func (m *NoneMethod) DialEarlyConn(conn net.Conn, destination M.Socksaddr) net.Conn {
	return &noneConn{
		Conn:        conn,
		destination: destination,
	}
}

func (m *NoneMethod) DialPacketConn(conn net.Conn) N.NetPacketConn {
	return &nonePacketConn{conn}
}

type noneConn struct {
	net.Conn

	access      sync.Mutex
	handshake   bool
	destination M.Socksaddr
}

func (c *noneConn) clientHandshake() error {
	err := M.SocksaddrSerializer.WriteAddrPort(c.Conn, c.destination)
	if err != nil {
		return err
	}
	c.handshake = true
	return nil
}

func (c *noneConn) Write(b []byte) (n int, err error) {
	if c.handshake {
		return c.Conn.Write(b)
	}
	err = M.SocksaddrSerializer.WriteAddrPort(c.Conn, c.destination)
	if err != nil {
		return
	}
	c.handshake = true
	return c.Conn.Write(b)
}

func (c *noneConn) WriteBuffer(buffer *buf.Buffer) error {
	defer buffer.Release()
	if c.handshake {
		return common.Error(c.Conn.Write(buffer.Bytes()))
	}

	header := buf.With(buffer.ExtendHeader(M.SocksaddrSerializer.AddrPortLen(c.destination)))
	err := M.SocksaddrSerializer.WriteAddrPort(header, c.destination)
	if err != nil {
		return err
	}
	c.handshake = true
	return common.Error(c.Conn.Write(buffer.Bytes()))
}

func (c *noneConn) ReadFrom(r io.Reader) (n int64, err error) {
	if c.handshake {
		return bufio.ReadFrom0(c, r)
	}
	return bufio.Copy(c.Conn, r)
}

func (c *noneConn) WriteTo(w io.Writer) (n int64, err error) {
	return bufio.Copy(w, c.Conn)
}

func (c *noneConn) RemoteAddr() net.Addr {
	return c.destination.TCPAddr()
}

type nonePacketConn struct {
	net.Conn
}

func (c *nonePacketConn) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	_, err := buffer.ReadFrom(c)
	if err != nil {
		return M.Socksaddr{}, err
	}
	return M.SocksaddrSerializer.ReadAddrPort(buffer)
}

func (c *nonePacketConn) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	defer buffer.Release()
	header := buf.With(buffer.ExtendHeader(M.SocksaddrSerializer.AddrPortLen(destination)))
	err := M.SocksaddrSerializer.WriteAddrPort(header, destination)
	if err != nil {
		return err
	}
	return common.Error(buffer.WriteTo(c))
}

func (c *nonePacketConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	n, err = c.Read(p)
	if err != nil {
		return
	}
	buffer := buf.With(p[:n])
	destination, err := M.SocksaddrSerializer.ReadAddrPort(buffer)
	if err != nil {
		return
	}
	addr = destination.UDPAddr()
	n = copy(p, buffer.Bytes())
	return
}

func (c *nonePacketConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	destination := M.SocksaddrFromNet(addr)
	_buffer := buf.StackNewSize(M.SocksaddrSerializer.AddrPortLen(destination) + len(p))
	defer common.KeepAlive(_buffer)
	buffer := common.Dup(_buffer)
	defer buffer.Release()
	err = M.SocksaddrSerializer.WriteAddrPort(buffer, destination)
	if err != nil {
		return
	}
	_, err = buffer.Write(p)
	if err != nil {
		return
	}
	return len(p), nil
}

type NoneService struct {
	handler Handler
	udp     *udpnat.Service[netip.AddrPort]
}

func NewNoneService(udpTimeout int64, handler Handler) Service {
	s := &NoneService{
		handler: handler,
	}
	s.udp = udpnat.New[netip.AddrPort](udpTimeout, s)
	return s
}

func (s *NoneService) NewConnection(ctx context.Context, conn net.Conn, metadata M.Metadata) error {
	destination, err := M.SocksaddrSerializer.ReadAddrPort(conn)
	if err != nil {
		return err
	}
	metadata.Protocol = "shadowsocks"
	metadata.Destination = destination
	return s.handler.NewConnection(ctx, conn, metadata)
}

func (s *NoneService) NewPacket(ctx context.Context, conn N.PacketConn, buffer *buf.Buffer, metadata M.Metadata) error {
	destination, err := M.SocksaddrSerializer.ReadAddrPort(buffer)
	if err != nil {
		return err
	}
	metadata.Protocol = "shadowsocks"
	metadata.Destination = destination
	s.udp.NewPacket(ctx, metadata.Source.AddrPort(), func() N.PacketWriter {
		return &nonePacketWriter{conn, metadata.Source}
	}, buffer, metadata)
	return nil
}

type nonePacketWriter struct {
	N.PacketConn
	sourceAddr M.Socksaddr
}

func (s *nonePacketWriter) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	header := buf.With(buffer.ExtendHeader(M.SocksaddrSerializer.AddrPortLen(destination)))
	err := M.SocksaddrSerializer.WriteAddrPort(header, destination)
	if err != nil {
		buffer.Release()
		return err
	}
	return s.PacketConn.WritePacket(buffer, s.sourceAddr)
}

func (s *NoneService) NewPacketConnection(ctx context.Context, conn N.PacketConn, metadata M.Metadata) error {
	return s.handler.NewPacketConnection(ctx, conn, metadata)
}

func (s *NoneService) HandleError(err error) {
	s.handler.HandleError(err)
}