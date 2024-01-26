package client

import (
	"context"
	"eiproxy/protocol"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"
)

const (
	proxyMasterAddr = "127.0.0.1:28004"
)

func runMasterTCPProxy(ctx context.Context, masterAddr string) error {
	var lc net.ListenConfig
	conn, err := lc.Listen(ctx, "tcp4", proxyMasterAddr)
	if err != nil {
		return fmt.Errorf("master TCP proxy: failed to listen: %w", err)
	}
	defer conn.Close()

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	for {
		clientConn, err := conn.Accept()
		if err != nil {
			if isCancelledOrClosed(err) {
				return nil
			}
			return fmt.Errorf("master TCP proxy: failed to accept: %w", err)
		}

		log.Printf("Master TCP proxy: accepted connection from %v", clientConn.RemoteAddr())

		go func() {
			defer clientConn.Close()

			// Connect to real master server.
			var d net.Dialer
			masterConn, err := d.DialContext(ctx, "tcp4", masterAddr)
			if err != nil {
				log.Printf("Master TCP proxy: failed to dial: %v", err)
				return
			}
			defer masterConn.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			go func() {
				<-ctx.Done()
				masterConn.Close()
				clientConn.Close()
			}()

			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				defer clientConn.(*net.TCPConn).CloseRead()
				_, err := io.Copy(masterConn, clientConn)
				if err != nil {
					if err = ignoreCancelledOrClosed(err); err != nil {
						log.Printf("Master TCP proxy: failed to copy: %v", err)
					}
				}
				log.Printf("Master TCP proxy: client <- master closed")
			}()
			go func() {
				defer wg.Done()
				defer clientConn.(*net.TCPConn).CloseWrite()
				_, err = io.Copy(clientConn, masterConn)
				if err != nil {
					if err = ignoreCancelledOrClosed(err); err != nil {
						log.Printf("Master TCP proxy: failed to copy: %v", err)
					}
				}
				log.Printf("Master TCP proxy: client -> master closed")
			}()
			wg.Wait()
		}()
	}
}

func runMasterUDPProxy(
	ctx context.Context,
	masterAddr *net.UDPAddr,
	dataToGameCh <-chan []byte,
	dataToServerCh chan<- []byte,
) error {
	gameAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8888}

	var lc net.ListenConfig
	pc, err := lc.ListenPacket(ctx, "udp4", proxyMasterAddr)
	if err != nil {
		return fmt.Errorf("master UDP proxy: failed to listen: %w", err)
	}
	defer pc.Close()

	go func() {
		<-ctx.Done()
		pc.Close()
	}()

	conn := pc.(*net.UDPConn)

	var wg sync.WaitGroup
	wg.Add(2)

	// Run server to game writer.
	go func() {
		defer wg.Done()
		defer conn.Close()
		for {
			var data []byte
			var ok bool
			select {
			case <-ctx.Done():
				return
			case data, ok = <-dataToGameCh:
				if !ok {
					return
				}
			}

			_, err = conn.WriteToUDP(data, gameAddr)
			if err != nil {
				if isCancelledOrClosed(err) {
					return
				}
				log.Printf("Master UDP proxy: failed to write: %v", err)
			}
		}
	}()

	// Run from game to server reader.
	go func() {
		defer wg.Done()
		defer conn.Close()
		var buf [2048]byte
		for {
			n, addr, err := conn.ReadFromUDP(buf[:])
			if err != nil {
				if isCancelledOrClosed(err) {
					return
				}
				if errors.Is(err, os.ErrDeadlineExceeded) {
					continue
				}

				log.Printf("Master UDP proxy: failed to read: %v", err)
				// Not sure if we should continue here, because it might be non-recoverable error.
				// But let's keep it like this for now until we see a real error.
				time.Sleep(100 * time.Millisecond) // prevent busy loop
				continue
			}

			if !addr.IP.Equal(addr.IP) || addr.Port != gameAddr.Port {
				log.Printf("Master UDP proxy: packet from unexpected addr: %v", addr)
				continue
			}

			if n == 0 {
				// Empty packets are currently not supported.
				continue
			}

			data := make([]byte, 0, n+protocol.AddrSize)
			data = protocol.EncodeAddrData(data, masterAddr, buf[:n])
			select {
			case dataToServerCh <- data:
			default:
				log.Printf("Master UDP proxy: data channel is full")
			}
		}
	}()

	wg.Wait()
	return nil
}
