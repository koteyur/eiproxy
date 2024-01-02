package protocol

import (
	"bytes"
	"net"
	"testing"
)

func TestEncodeAddrData(t *testing.T) {
	t.Run("EncodeAddrData", func(t *testing.T) {
		addr := &net.UDPAddr{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: 12345,
		}
		data := []byte{1, 2, 3, 4, 5, 6, 7, 8}
		expected := []byte{127, 0, 0, 1, 57, 48, 1, 2, 3, 4, 5, 6, 7, 8}

		actual := EncodeAddrData(nil, addr, data)
		if !bytes.Equal(expected, actual) {
			t.Errorf("Expected %v, got %v", expected, actual)
		}
	})

	t.Run("EncodeAddrDataPanicIPv4", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatalf("Expected panic")
			}
		}()

		addr := &net.UDPAddr{
			IP:   net.IPv6loopback,
			Port: 12345,
		}
		data := []byte{1, 2, 3, 4, 5, 6, 7, 8}

		EncodeAddrData(nil, addr, data)
	})

	t.Run("EncodeAddrDataPanicEmptyData", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatalf("Expected panic")
			}
		}()

		addr := &net.UDPAddr{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: 12345,
		}
		data := []byte{}

		EncodeAddrData(nil, addr, data)
	})
}

func TestDecodeAddrData(t *testing.T) {
	t.Run("DecodeAddrData", func(t *testing.T) {
		data := []byte{127, 0, 0, 1, 57, 48, 1, 2, 3, 4, 5, 6, 7, 8}
		expectedAddr := &net.UDPAddr{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: 12345,
		}
		expectedData := []byte{1, 2, 3, 4, 5, 6, 7, 8}

		actualAddr, actualData := DecodeAddrData(data)
		if !actualAddr.IP.Equal(expectedAddr.IP) || actualAddr.Port != expectedAddr.Port {
			t.Errorf("Expected %v, got %v", expectedAddr, actualAddr)
		}
		if !bytes.Equal(expectedData, actualData) {
			t.Errorf("Expected %v, got %v", expectedData, actualData)
		}
	})

	t.Run("DecodeAddrDataPanicShortData", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatalf("Expected panic")
			}
		}()

		data := []byte{127, 0, 0, 1, 57, 48}

		DecodeAddrData(data)
	})
}
