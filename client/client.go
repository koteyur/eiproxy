package client

import (
	"context"
	"eiproxy/protocol"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
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

type client struct {
	mut   sync.Mutex
	cfg   Config
	ready chan struct{}

	dataToServerCh     chan []byte
	remoteAddrToDataCh map[addrPortV4]chan []byte
	remoteIPToLocalIP  map[ipv4]ipv4
	nextLocalIP        ipv4
	masterAddr         *net.UDPAddr
	serverIP           *net.IPAddr
	token              protocol.Token
	port               int
}

func New(cfg Config) *client {
	return &client{
		cfg:                cfg,
		dataToServerCh:     make(chan []byte, dataChanSize),
		remoteIPToLocalIP:  make(map[ipv4]ipv4),
		remoteAddrToDataCh: make(map[addrPortV4]chan []byte, dataChanSize),
		nextLocalIP:        ipv4{127, 0, 0, 1},
		ready:              make(chan struct{}),
	}
}

func (c *client) Run(ctx context.Context) error {
	serverURL, err := url.Parse(c.cfg.ServerURL)
	if err != nil {
		return fmt.Errorf("failed to parse server url: %w", err)
	}

	log.Printf("Resolving master server address %s", c.cfg.MasterAddr)
	masterAddr, err := net.ResolveUDPAddr("udp4", c.cfg.MasterAddr)
	if err != nil {
		return fmt.Errorf("failed to resolve master address: %w", err)
	}
	c.masterAddr = masterAddr

	log.Printf("Resolving server address %s", serverURL.Hostname())
	serverIP, err := net.ResolveIPAddr("ip4", serverURL.Hostname())
	if err != nil {
		return fmt.Errorf("failed to resolve server address: %w", err)
	}
	c.serverIP = serverIP

	log.Printf("Connecting to server %#v", c.cfg.ServerURL)
	port, token, err := c.connect(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	log.Printf("Connection established. Port: %d", port)
	c.token = token
	c.port = port
	close(c.ready)

	var wg sync.WaitGroup
	defer wg.Wait()

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	run := func(f func() error, prefix string) {
		wg.Add(1)
		go func() {
			defer wg.Done()

			var err error
			defer func() { cancel(err) }()
			err = f()
			log.Printf("%s: stopped: %v", prefix, err)
			err = ignoreCancelledOrClosed(err)
			if err != nil {
				err = fmt.Errorf("%s: %v", strings.ToLower(prefix), err)
			}
		}()
	}

	run(func() error { return runMasterProxy(ctx, c.cfg.MasterAddr) }, "Master proxy")
	run(func() error {
		return c.runProxyClient(ctx, fmt.Sprintf("%s:%d", serverURL.Hostname(), port))
	}, "Proxy main loop")

	<-ctx.Done()
	return context.Cause(ctx)
}

func (c *client) GetProxyAddr(timeout time.Duration) string {
	select {
	case <-c.ready:
		return fmt.Sprintf("%s:%d", c.serverIP.IP, c.port)
	case <-time.After(timeout):
		return ""
	}
}

func (c *client) connect(ctx context.Context) (port int, token protocol.Token, err error) {
	url, err := url.JoinPath(c.cfg.ServerURL, "api/connect")
	if err != nil {
		return 0, protocol.Token{}, fmt.Errorf("failed to join url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return 0, protocol.Token{}, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.cfg.UserKey))

	hc := http.Client{
		Timeout: 5 * time.Second,
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, protocol.Token{}, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, protocol.Token{}, fmt.Errorf("server returned %d", resp.StatusCode)
	}

	var connResp protocol.ConnectionResponse
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&connResp); err != nil {
		return 0, protocol.Token{}, fmt.Errorf("failed to decode response: %w", err)
	}

	if connResp.ErrorCode != nil {
		return 0, protocol.Token{}, fmt.Errorf("server returned error: %v", *connResp.ErrorCode)
	}
	if connResp.ErrorMessage != nil {
		return 0, protocol.Token{}, fmt.Errorf("server returned error: %v", *connResp.ErrorMessage)
	}
	if connResp.Port == nil || connResp.Token == nil {
		return 0, protocol.Token{}, fmt.Errorf("server returned invalid response: %v", connResp)
	}

	return *connResp.Port, *connResp.Token, nil
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

func (c *client) proxyMainLoopReader(ctx context.Context, conn *net.UDPConn) error {
	defer func() {
		c.mut.Lock()
		defer c.mut.Unlock()
		clear(c.remoteAddrToDataCh)
	}()

	c.getWorkerChan(c.masterAddr, true)

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
		if n >= protocol.AddrDataMinSize {
			addr, data := protocol.DecodeAddrData(buf[:n])
			dataCh := c.getWorkerChan(addr, false)
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
	remoteAddr *net.UDPAddr,
	localIP net.IP,
	dataCh <-chan []byte,
	isMaster bool,
) error {
	gameAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8888}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	port := 0
	if isMaster {
		port = 28004
	}

	var lc net.ListenConfig
	pc, err := lc.ListenPacket(ctx, "udp4", fmt.Sprintf("%s:%d", localIP, port))
	if err != nil {
		return fmt.Errorf("worker: failed to listen: %w", err)
	}
	defer pc.Close()

	conn := pc.(*net.UDPConn)

	log.Printf("Running worker: local addr: %v, remote addr: %v is master: %v",
		conn.LocalAddr(), remoteAddr, isMaster)

	var wg sync.WaitGroup
	wg.Add(2)

	// Run server to game writer.
	go func() {
		defer wg.Done()
		defer conn.Close()
		for data := range dataCh {
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
			if !isMaster {
				err := conn.SetReadDeadline(time.Now().Add(30 * time.Second))
				if err != nil {
					if err = ignoreCancelledOrClosed(err); err != nil {
						log.Printf("Worker: failed to set read deadline: %v", err)
					}
					return
				}
			}

			n, addr, err := conn.ReadFromUDP(buf[:])
			if err != nil {
				if isCancelledOrClosed(err) {
					return
				}
				if errors.Is(err, os.ErrDeadlineExceeded) {
					if !isMaster {
						log.Printf("Worker: timed out, exiting. local addr: %v, remote addr: %v",
							conn.LocalAddr(), remoteAddr)
						return
					}
					continue
				}

				log.Printf("Worker: failed to read: %v", err)
				if isMaster {
					// Not sure if we should continue here, but it's might be non-recoverable error.
					// But let's keep it like this for now until we see a real error.
					time.Sleep(100 * time.Millisecond) // prevent busy loop
					continue
				}
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

			data := make([]byte, 0, n+protocol.AddrDataMinSize-1)
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

func (c *client) getWorkerChan(addr *net.UDPAddr, isMaster bool) chan []byte {
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

	go func(dataCh chan []byte) {
		err := c.handleWorker(addr, localIP.ToIP(), dataCh, isMaster)
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

func runMasterProxy(ctx context.Context, masterAddr string) error {
	var lc net.ListenConfig
	conn, err := lc.Listen(ctx, "tcp4", "127.0.0.1:28004")
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
