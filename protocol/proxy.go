package protocol

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
)

var ErrInvalidToken = errors.New("invalid token")

const AddrDataMinSize = 4 /*ipv4*/ + 2 /*port*/ + 1 /*min data*/

// Temporary token to auth in UDP proxy.
type Token [AddrDataMinSize - 1]byte

func NewToken() (Token, error) {
	var token Token
	_, err := rand.Read(token[:])
	return token, err
}

func EncodeAddrData(buf []byte, addr *net.UDPAddr, data []byte) []byte {
	ipv4 := addr.IP.To4()
	if ipv4 == nil {
		panic("only ipv4 is supported")
	}
	if len(data) == 0 {
		panic("data must not be empty")
	}

	buf = append(buf, ipv4...)
	buf = binary.LittleEndian.AppendUint16(buf, uint16(addr.Port))
	buf = append(buf, data...)
	return buf
}

func DecodeAddrData(data []byte) (*net.UDPAddr, []byte) {
	if len(data) < AddrDataMinSize {
		panic("data is too short")
	}
	return &net.UDPAddr{
		IP:   net.IPv4(data[0], data[1], data[2], data[3]),
		Port: int(binary.LittleEndian.Uint16(data[4:6])),
	}, data[6:]
}

type ProxyClientRequestType byte

const (
	ProxyClientRequestTypeKeepAlive  ProxyClientRequestType = 'k'
	ProxyClientRequestTypeDisconnect ProxyClientRequestType = 'd'
)

type ProxyServerResponseType byte

const (
	ProxyServerResponseTypeKeepAlive  ProxyServerResponseType = 'K'
	ProxyServerResponseTypeDisconnect ProxyServerResponseType = 'D'
)
