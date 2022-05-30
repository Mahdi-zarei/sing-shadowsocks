package shadowsocks

import (
	"context"
	"crypto/md5"
	"fmt"
	"net"

	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

var (
	ErrBadKey          = E.New("bad key")
	ErrMissingPassword = E.New("missing password")
)

type Method interface {
	Name() string
	KeyLength() int
	DialConn(conn net.Conn, destination M.Socksaddr) (net.Conn, error)
	DialEarlyConn(conn net.Conn, destination M.Socksaddr) net.Conn
	DialPacketConn(conn net.Conn) N.NetPacketConn
}

type Service interface {
	N.TCPConnectionHandler
	N.UDPHandler
}

type Handler interface {
	N.TCPConnectionHandler
	N.UDPConnectionHandler
	E.Handler
}

type UserContext[U comparable] struct {
	context.Context
	User U
}

type ServerConnError struct {
	net.Conn
	Source M.Socksaddr
	Cause  error
}

func (e *ServerConnError) Close() error {
	if conn, ok := common.Cast[*net.TCPConn](e.Conn); ok {
		conn.SetLinger(0)
	}
	return e.Conn.Close()
}

func (e *ServerConnError) Unwrap() error {
	return e.Cause
}

func (e *ServerConnError) Error() string {
	return fmt.Sprint("shadowsocks: serve TCP from ", e.Source, ": ", e.Cause)
}

type ServerPacketError struct {
	Source M.Socksaddr
	Cause  error
}

func (e *ServerPacketError) Unwrap() error {
	return e.Cause
}

func (e *ServerPacketError) Error() string {
	return fmt.Sprint("shadowsocks: serve UDP from ", e.Source, ": ", e.Cause)
}

func Key(password []byte, keySize int) []byte {
	var b, prev []byte
	h := md5.New()
	for len(b) < keySize {
		h.Write(prev)
		h.Write([]byte(password))
		b = h.Sum(b)
		prev = b[len(b)-h.Size():]
		h.Reset()
	}
	return b[:keySize]
}