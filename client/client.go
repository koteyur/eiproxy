package client

import (
	"context"
	"eiproxy/protocol"
	"fmt"
	"log"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	ClientVer   = "0.3.0"
	ProtocolVer = "1.0"
)

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

type Client interface {
	Run(ctx context.Context) error
	GetProxyAddr(timeout time.Duration) string
	GetUser(ctx context.Context) (protocol.UserResponse, error)
}

func New(cfg Config) Client {
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
