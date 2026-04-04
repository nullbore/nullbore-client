package tunnel

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"

	"github.com/nullbore/nullbore-client/internal/debug"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nullbore/nullbore-client/internal/config"
)

// Connector manages the control WebSocket and spawns data connections.
// It handles automatic reconnection with exponential backoff.
type Connector struct {
	cfg       *config.Config
	tunnelID  string
	localPort int
	localHost string // target host (default "127.0.0.1", can be docker service name)
	control   *websocket.Conn
	mu        sync.Mutex
	closed    bool
}

// Control channel message (matches server protocol)
type controlMessage struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
}

func NewConnector(cfg *config.Config, tunnelID string, localPort int) *Connector {
	return &Connector{
		cfg:       cfg,
		tunnelID:  tunnelID,
		localPort: localPort,
		localHost: "127.0.0.1",
	}
}

// NewConnectorWithHost creates a Connector targeting a specific host (for Docker/network use).
func NewConnectorWithHost(cfg *config.Config, tunnelID string, localHost string, localPort int) *Connector {
	if localHost == "" {
		localHost = "127.0.0.1"
	}
	return &Connector{
		cfg:       cfg,
		tunnelID:  tunnelID,
		localPort: localPort,
		localHost: localHost,
	}
}

// connect establishes the control WebSocket to the server.
func (c *Connector) connect() error {
	wsURL := httpToWS(c.cfg.ServerURL())
	controlURL := fmt.Sprintf("%s/ws/control?tunnel_id=%s", wsURL, url.QueryEscape(c.tunnelID))

	header := http.Header{}
	if token := c.cfg.Token(); token != "" {
		header.Set("Authorization", "Bearer "+token)
	}

	debug.Printf("connecting control channel to %s", controlURL)

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}
	if c.cfg.InsecureSkipVerify() {
		dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	conn, _, err := dialer.Dial(controlURL, header)
	if err != nil {
		return fmt.Errorf("control connect: %w", err)
	}

	c.mu.Lock()
	c.control = conn
	c.mu.Unlock()

	debug.Printf("control connected: tunnel=%s", c.tunnelID)
	return nil
}

// Connect establishes the initial control WebSocket (convenience for first connection).
func (c *Connector) Connect() error {
	return c.connect()
}

// RunWithReconnect runs the control loop with automatic reconnection.
// It will keep trying to reconnect until Close() is called or maxRetries is exceeded.
// Set maxRetries to -1 for unlimited retries.
func (c *Connector) RunWithReconnect(maxRetries int) error {
	backoff := NewBackoff()
	var consecutiveFailures int

	for {
		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		if closed {
			return nil
		}

		// Run the control loop — blocks until disconnection
		connected, err := c.runOnce()

		c.mu.Lock()
		closed = c.closed
		c.mu.Unlock()
		if closed {
			return nil
		}

		// If we were connected for >10s, it was a real session — reset backoff
		if connected {
			backoff.Reset()
			consecutiveFailures = 0
			if err != nil {
				log.Printf("disconnected: %v", err)
			} else {
				log.Printf("disconnected (clean)")
			}
		} else {
			consecutiveFailures++
			if err != nil {
				log.Printf("connection failed (attempt %d): %v", int(backoff.Attempt()), err)
			}
		}

		// Check retry limit
		if maxRetries >= 0 && consecutiveFailures > maxRetries {
			return fmt.Errorf("max retries (%d) exceeded", maxRetries)
		}

		// Wait before reconnecting
		d := backoff.Duration()
		log.Printf("reconnecting in %s...", d)
		time.Sleep(d)

		// Reconnect
		if err := c.connect(); err != nil {
			continue // will retry on next iteration
		}
	}
}

// runOnce runs a single control loop session. Returns whether we were successfully
// connected (for >10s), and any error.
func (c *Connector) runOnce() (connected bool, err error) {
	c.mu.Lock()
	conn := c.control
	c.mu.Unlock()

	if conn == nil {
		return false, fmt.Errorf("no control connection")
	}

	start := time.Now()

	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// Ping ticker to keep connection alive
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.mu.Lock()
				ctrl := c.control
				c.mu.Unlock()
				if ctrl != nil {
					ctrl.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
				}
			case <-done:
				return
			}
		}
	}()

	defer close(done)

	for {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		_, message, err := conn.ReadMessage()
		if err != nil {
			wasConnected := time.Since(start) > 10*time.Second
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				return wasConnected, fmt.Errorf("control read: %w", err)
			}
			return wasConnected, nil
		}

		var msg controlMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			debug.Printf("invalid control message: %v", err)
			continue
		}

		switch msg.Type {
		case "connection":
			go c.handleConnection(msg.ID)
		default:
			debug.Printf("unknown control message type: %s", msg.Type)
		}
	}
}

// Run runs without reconnect (for backward compat / simple usage).
func (c *Connector) Run() error {
	_, err := c.runOnce()
	return err
}

// Close signals the connector to stop and closes the control connection.
func (c *Connector) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	if c.control != nil {
		c.control.Close()
	}
}

// handleConnection opens a data WebSocket to the server and pipes it to localhost.
func (c *Connector) handleConnection(connID string) {
	wsURL := httpToWS(c.cfg.ServerURL())
	dataURL := fmt.Sprintf("%s/ws/data?id=%s", wsURL, url.QueryEscape(connID))

	// Open data WebSocket to server
	dataDialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}
	if c.cfg.InsecureSkipVerify() {
		dataDialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	dataWS, _, err := dataDialer.Dial(dataURL, nil)
	if err != nil {
		debug.Printf("data connect error: id=%s err=%v", connID, err)
		return
	}

	// Connect to local service
	localAddr := fmt.Sprintf("%s:%d", c.localHost, c.localPort)
	localConn, err := net.DialTimeout("tcp", localAddr, 5*time.Second)
	if err != nil {
		debug.Printf("local connect error: id=%s addr=%s err=%v", connID, localAddr, err)
		dataWS.WriteMessage(websocket.BinaryMessage,
			[]byte("HTTP/1.1 502 Bad Gateway\r\nContent-Length: 22\r\n\r\nlocal service refused\n"))
		dataWS.Close()
		return
	}

	debug.Printf("relaying: id=%s → %s", connID, localAddr)

	// Pipe: data WebSocket ↔ local TCP connection
	dataConn := NewWSNetConn(dataWS)
	pipe(localConn, dataConn)

	debug.Printf("relay closed: id=%s", connID)
}

// WSNetConn wraps a websocket.Conn into a net.Conn for raw byte streaming.
type WSNetConn struct {
	conn *websocket.Conn
	buf  []byte
}

func NewWSNetConn(conn *websocket.Conn) *WSNetConn {
	return &WSNetConn{conn: conn}
}

func (c *WSNetConn) Read(dst []byte) (int, error) {
	if len(c.buf) > 0 {
		n := copy(dst, c.buf)
		c.buf = c.buf[n:]
		return n, nil
	}
	_, msg, err := c.conn.ReadMessage()
	if err != nil {
		return 0, err
	}
	n := copy(dst, msg)
	if n < len(msg) {
		c.buf = make([]byte, len(msg)-n)
		copy(c.buf, msg[n:])
	}
	return n, nil
}

func (c *WSNetConn) Write(b []byte) (int, error) {
	if err := c.conn.WriteMessage(websocket.BinaryMessage, b); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (c *WSNetConn) Close() error {
	return c.conn.Close()
}

// pipe copies data bidirectionally between two connections.
func pipe(a, b io.ReadWriteCloser) {
	var wg sync.WaitGroup
	wg.Add(2)

	cp := func(dst io.WriteCloser, src io.Reader) {
		defer wg.Done()
		io.Copy(dst, src)
		dst.Close()
	}

	go cp(a, b)
	go cp(b, a)

	wg.Wait()
	a.Close()
	b.Close()
}

func httpToWS(serverURL string) string {
	u := strings.Replace(serverURL, "https://", "wss://", 1)
	u = strings.Replace(u, "http://", "ws://", 1)
	return u
}
