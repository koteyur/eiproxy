package client

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

const (
	proxyMasterAddr = "127.0.0.1:28004"
)

func runMasterProxy(ctx context.Context, masterAddr string) error {
	var lc net.ListenConfig
	conn, err := lc.Listen(ctx, "tcp4", proxyMasterAddr)
	if err != nil {
		return fmt.Errorf("master proxy: failed to listen: %w", err)
	}
	defer conn.Close()

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	for {
		clientConn, err := conn.Accept()
		if err != nil {
			return fmt.Errorf("master proxy: failed to accept: %w", err)
		}

		log.Printf("Master proxy: accepted connection from %v", clientConn.RemoteAddr())

		go func() {
			defer clientConn.Close()

			// Connect to real master server.
			var d net.Dialer
			masterConn, err := d.DialContext(ctx, "tcp4", masterAddr)
			if err != nil {
				log.Printf("master proxy: failed to dial: %v", err)
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
						log.Printf("master proxy: failed to copy: %v", err)
					}
				}
				log.Printf("master proxy: client <- master closed")
			}()
			go func() {
				defer wg.Done()
				defer clientConn.(*net.TCPConn).CloseWrite()
				_, err = io.Copy(clientConn, masterConn)
				if err != nil {
					if err = ignoreCancelledOrClosed(err); err != nil {
						log.Printf("master proxy: failed to copy: %v", err)
					}
				}
				log.Printf("master proxy: client -> master closed")
			}()
			wg.Wait()
		}()
	}
}
