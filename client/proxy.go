package client

import (
	"context"
	"eiproxy/protocol"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

const dataChanSize = 1000

type ipv4 [net.IPv4len]byte

func (ip ipv4) ToIP() net.IP {
	return net.IPv4(ip[0], ip[1], ip[2], ip[3])
}

func (ip ipv4) Next() ipv4 {
	ipnum := binary.BigEndian.Uint32(ip[:])
	ipnum++
	binary.BigEndian.PutUint32(ip[:], ipnum)
	return ip
}

type addrPortV4 struct {
	ip   ipv4
	port uint16
}

func (ap addrPortV4) ToUDPAddr() *net.UDPAddr {
	return &net.UDPAddr{
		IP:   ap.ip.ToIP(),
		Port: int(ap.port),
	}
}

func (c *client) runProxyClient(ctx context.Context, addr string) error {
	var d net.Dialer
	netConn, err := d.DialContext(ctx, "udp4", addr)
	if err != nil {
		return fmt.Errorf("failed to dial: %w", err)
	}
	defer netConn.Close()
	conn := netConn.(*net.UDPConn)

	log.Printf("Sending token to %#v", addr)
	err = sendToken(conn, c.token)
	if err != nil {
		return fmt.Errorf("failed to send token: %w", err)
	}
	log.Printf("Token has been sent")

	var wg sync.WaitGroup
	defer wg.Wait() // wait after context is cancelled and dataToServerCh is closed

	childCtx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	go func() {
		<-childCtx.Done()
		conn.Close()
	}()

	run := func(f func(ctx context.Context, conn *net.UDPConn) error, prefix string) {
		wg.Add(1)
		go func() {
			defer wg.Done()

			var err error
			defer func() { cancel(err) }()

			err = f(childCtx, conn)
			log.Printf("%s: stopped: %v", prefix, err)

			err = ignoreCancelledOrClosed(err)
			if err != nil {
				err = fmt.Errorf("%s: %v", strings.ToLower(prefix), err)
			}
		}()
	}

	run(c.proxyMainLoopReader, "Main loop reader")
	run(c.proxyMainLoopWriter, "Main loop writer")

	select {
	case <-ctx.Done():
		// Graceful shutdown.

	case <-childCtx.Done():
		// They can't decide to stop by themselves, so something happend.
		err := context.Cause(childCtx)
		log.Printf("Client failed: %v", err)
		return err
	}

	log.Printf("Context done, disconnecting")
	for retry := 0; retry < 10; retry++ {
		c.dataToServerCh <- []byte{byte(protocol.ProxyClientRequestTypeDisconnect)}

		select {
		case <-childCtx.Done():
			// Assume that server has disconnected.
			log.Printf("Disconnected from proxy server")
			return nil
		case <-time.After(100 * time.Millisecond):
		}
	}

	return fmt.Errorf("failed to disconnect")
}

func sendToken(conn *net.UDPConn, token protocol.Token) error {
	err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err != nil {
		return fmt.Errorf("token: failed to set deadline: %w", err)
	}

	var buf [2048]byte
	for {
		_, err = conn.Write(token[:])
		if err != nil {
			return fmt.Errorf("token: failed to write: %w", err)
		}

		err := conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		if err != nil {
			return fmt.Errorf("token: failed to set deadline: %w", err)
		}

		n, err := conn.Read(buf[:])
		if err != nil {
			if !errors.Is(err, os.ErrDeadlineExceeded) {
				return fmt.Errorf("token: failed to read: %w", err)
			}
		} else if n > 0 && buf[0] == byte(protocol.ProxyServerResponseTypeKeepAlive) {
			return nil
		}

		time.Sleep(100 * time.Millisecond)
	}
}

func (c *client) proxyMainLoopReader(ctx context.Context, conn *net.UDPConn) (err error) {
	var wg sync.WaitGroup
	defer wg.Wait()

	ctx, cancel := context.WithCancelCause(ctx)
	defer func() { cancel(err) }()

	defer func() {
		c.mut.Lock()
		defer c.mut.Unlock()
		c.remoteAddrToDataCh = make(map[addrPortV4]chan []byte, dataChanSize)
	}()

	masterAddrPortV4 := addrPortV4{
		ip:   ipv4(c.masterAddr.IP.To4()[:net.IPv4len]),
		port: uint16(c.masterAddr.Port),
	}
	masterDataCh := make(chan []byte, dataChanSize)
	c.remoteAddrToDataCh[masterAddrPortV4] = masterDataCh
	go func() {
		err := runMasterUDPProxy(ctx, c.masterAddr, masterDataCh, c.dataToServerCh)
		log.Printf("Master UDP proxy failed: %v", err)
	}()

	lastSuccess := time.Now()
	var buf [2048]byte
	for {
		err := conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		if err != nil {
			return fmt.Errorf("main-loop: failed to set read deadline: %w", err)
		}

		n, err := conn.Read(buf[:])
		if err != nil {
			if !errors.Is(err, os.ErrDeadlineExceeded) {
				return fmt.Errorf("main-loop: failed to read: %w", err)
			}

			if time.Since(lastSuccess) > 30*time.Second {
				log.Printf("Main loop: server stopped responding")
				return fmt.Errorf("main-loop: server stopped responding")
			}

			log.Printf("Main loop: server read timeout, sending token")
			select {
			case <-ctx.Done():
				return ctx.Err()

			// This might get stuck if main writer exited.
			case c.dataToServerCh <- c.token[:]:
			}
			continue
		}

		lastSuccess = time.Now()

		if n == 0 {
			// Empty packets are currently not supported.
			continue
		}
		if n > protocol.AddrSize {
			addr, data := protocol.DecodeAddrData(buf[:n])
			dataCh := c.getWorkerChan(ctx, &wg, addr)
			select {
			case dataCh <- append([]byte(nil), data...):
			default:
				log.Printf("Main loop: data channel is full")
			}
		} else {
			switch protocol.ProxyServerResponseType(buf[0]) {
			case protocol.ProxyServerResponseTypeKeepAlive:
				log.Printf("Keep alive response")
			case protocol.ProxyServerResponseTypeDisconnect:
				log.Printf("Disconnect response")
				return nil
			default:
				log.Printf("Unexpected response %x", buf[0])
			}
		}
	}
}

func (c *client) proxyMainLoopWriter(ctx context.Context, conn *net.UDPConn) error {
	const keepAliveInterval = 3 * time.Second
	ticker := time.NewTicker(keepAliveInterval)
	defer ticker.Stop()

	err := conn.SetWriteDeadline(time.Time{})
	if err != nil {
		return fmt.Errorf("main-loop: failed to set write deadline: %w", err)
	}

	for {
		var data []byte
		var ok bool
		select {
		case <-ctx.Done():
			return ctx.Err()
		case data, ok = <-c.dataToServerCh:
			if !ok {
				return nil
			}
			ticker.Reset(keepAliveInterval)
		case <-ticker.C:
			data = []byte{byte(protocol.ProxyClientRequestTypeKeepAlive)}
		}

		_, err := conn.Write(data)
		if err != nil {
			return fmt.Errorf("main-loop: failed to write: %w", err)
		}
	}
}

func (c *client) handleWorker(
	ctx context.Context,
	remoteAddr *net.UDPAddr,
	localIP net.IP,
	dataCh <-chan []byte,
) error {
	gameAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8888}

	var lc net.ListenConfig
	pc, err := lc.ListenPacket(ctx, "udp4", fmt.Sprintf("%s:0", localIP))
	if err != nil {
		return fmt.Errorf("worker: failed to listen: %w", err)
	}
	defer pc.Close()

	go func() {
		<-ctx.Done()
		pc.Close()
	}()

	conn := pc.(*net.UDPConn)

	log.Printf("Running worker: local addr: %v, remote addr: %v", conn.LocalAddr(), remoteAddr)

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
			case data, ok = <-dataCh:
				if !ok {
					return
				}
			}

			_, err = conn.WriteToUDP(data, gameAddr)
			if err != nil {
				if isCancelledOrClosed(err) {
					return
				}
				log.Printf("Worker: failed to write: %v", err)
			}
		}
	}()

	// Run from game to server reader.
	go func() {
		defer wg.Done()
		defer conn.Close()
		var buf [2048]byte
		for {
			err := conn.SetReadDeadline(time.Now().Add(30 * time.Second))
			if err != nil {
				if err = ignoreCancelledOrClosed(err); err != nil {
					log.Printf("Worker: failed to set read deadline: %v", err)
				}
				return
			}

			n, addr, err := conn.ReadFromUDP(buf[:])
			if err != nil {
				if isCancelledOrClosed(err) {
					return
				}
				if errors.Is(err, os.ErrDeadlineExceeded) {
					log.Printf("Worker: timed out, exiting. local addr: %v, remote addr: %v",
						conn.LocalAddr(), remoteAddr)
					return
				}

				log.Printf("Worker: failed to read: %v", err)
				return
			}

			if !addr.IP.Equal(addr.IP) || addr.Port != gameAddr.Port {
				log.Printf("Worker: packet from unexpected addr: %v", addr)
				continue
			}

			if n == 0 {
				// Empty packets are currently not supported.
				continue
			}

			data := make([]byte, 0, n+protocol.AddrSize)
			data = protocol.EncodeAddrData(data, remoteAddr, buf[:n])
			select {
			case c.dataToServerCh <- data:
			default:
				log.Printf("Worker: data channel is full")
			}
		}
	}()

	wg.Wait()
	return nil
}

func (c *client) getWorkerChan(
	ctx context.Context,
	wg *sync.WaitGroup,
	addr *net.UDPAddr,
) chan []byte {

	ip := addr.IP.To4()
	if ip == nil {
		log.Printf("Received non-IPv4 address %v", addr)
		return nil
	}
	addr4 := addrPortV4{ipv4(ip), uint16(addr.Port)}

	c.mut.Lock()
	defer c.mut.Unlock()

	if dataCh, ok := c.remoteAddrToDataCh[addr4]; ok {
		return dataCh
	}

	log.Printf("Creating worker for %v", addr4)

	localIP, ok := c.remoteIPToLocalIP[addr4.ip]
	if !ok {
		localIP = c.nextLocalIP
		c.nextLocalIP = localIP.Next()
		c.remoteIPToLocalIP[addr4.ip] = localIP
	}

	dataCh := make(chan []byte, dataChanSize)
	c.remoteAddrToDataCh[addr4] = dataCh

	wg.Add(1)
	go func(dataCh chan []byte) {
		defer wg.Done()

		err := c.handleWorker(ctx, addr, localIP.ToIP(), dataCh)
		if err != nil {
			log.Printf("Worker for %v failed: %v", addr4, err)
		}

		c.mut.Lock()
		defer c.mut.Unlock()
		delete(c.remoteAddrToDataCh, addr4)
	}(dataCh)
	return dataCh
}

func ignoreCancelledOrClosed(err error) error {
	if err == nil || isCancelledOrClosed(err) {
		return nil
	}
	return err
}

func isCancelledOrClosed(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed)
}
