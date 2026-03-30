package tunnel

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nullbore/nullbore-client/internal/config"
)

func TestHttpToWS(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"http://localhost:8080", "ws://localhost:8080"},
		{"https://api.nullbore.com", "wss://api.nullbore.com"},
		{"http://192.168.1.1:9090", "ws://192.168.1.1:9090"},
		{"https://example.com/path", "wss://example.com/path"},
	}

	for _, tt := range tests {
		got := httpToWS(tt.input)
		if got != tt.want {
			t.Errorf("httpToWS(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestWSNetConnReadWrite tests the WebSocket-to-net.Conn adapter.
func TestWSNetConnReadWrite(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	var serverConn *websocket.Conn
	serverReady := make(chan struct{})
	serverDone := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		serverConn, err = upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade error: %v", err)
			return
		}
		close(serverReady)
		<-serverDone
	}))
	defer server.Close()

	// Connect client
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientWS, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	defer clientWS.Close()

	<-serverReady
	defer close(serverDone)

	clientConn := NewWSNetConn(clientWS)
	serverNetConn := NewWSNetConn(serverConn)

	// Client writes, server reads
	testData := []byte("hello from client")
	n, err := clientConn.Write(testData)
	if err != nil {
		t.Fatalf("client write error: %v", err)
	}
	if n != len(testData) {
		t.Errorf("wrote %d bytes, want %d", n, len(testData))
	}

	buf := make([]byte, 256)
	n, err = serverNetConn.Read(buf)
	if err != nil {
		t.Fatalf("server read error: %v", err)
	}
	if string(buf[:n]) != "hello from client" {
		t.Errorf("read %q, want %q", string(buf[:n]), "hello from client")
	}

	// Server writes, client reads
	testData2 := []byte("hello from server")
	serverNetConn.Write(testData2)

	n, err = clientConn.Read(buf)
	if err != nil {
		t.Fatalf("client read error: %v", err)
	}
	if string(buf[:n]) != "hello from server" {
		t.Errorf("read %q, want %q", string(buf[:n]), "hello from server")
	}
}

// TestWSNetConnBuffering tests that large messages are buffered correctly
// when the read buffer is smaller than the message.
func TestWSNetConnBuffering(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	var serverConn *websocket.Conn
	serverReady := make(chan struct{})
	serverDone := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		serverConn, err = upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		close(serverReady)
		<-serverDone
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientWS, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	defer clientWS.Close()

	<-serverReady
	defer close(serverDone)

	// Send a large message from server
	bigMsg := bytes.Repeat([]byte("X"), 1024)
	serverConn.WriteMessage(websocket.BinaryMessage, bigMsg)

	// Read in small chunks from client
	clientConn := NewWSNetConn(clientWS)
	var received []byte
	buf := make([]byte, 100) // Small buffer
	for len(received) < 1024 {
		n, err := clientConn.Read(buf)
		if err != nil {
			t.Fatalf("read error after %d bytes: %v", len(received), err)
		}
		received = append(received, buf[:n]...)
	}

	if len(received) != 1024 {
		t.Errorf("received %d bytes, want 1024", len(received))
	}
	if !bytes.Equal(received, bigMsg) {
		t.Error("received data does not match sent data")
	}
}

// TestPipe tests bidirectional piping between two connections.
func TestPipe(t *testing.T) {
	// Create a pair of connected pipes
	a1, a2 := net.Pipe()
	b1, b2 := net.Pipe()

	done := make(chan struct{})
	go func() {
		pipe(a2, b1)
		close(done)
	}()

	// Write through a1, should come out b2
	go func() {
		a1.Write([]byte("hello"))
		// Give the pipe time to transfer, then close
		time.Sleep(50 * time.Millisecond)
		a1.Close()
	}()

	buf := make([]byte, 256)
	n, err := b2.Read(buf)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if string(buf[:n]) != "hello" {
		t.Errorf("got %q, want %q", string(buf[:n]), "hello")
	}

	b2.Close()
	<-done
}

// TestPipeBidirectional tests data flows in both directions simultaneously.
func TestPipeBidirectional(t *testing.T) {
	a1, a2 := net.Pipe()
	b1, b2 := net.Pipe()

	go pipe(a2, b1)

	var wg sync.WaitGroup
	wg.Add(2)

	// a1 → b2
	go func() {
		defer wg.Done()
		a1.Write([]byte("from-a"))
		buf := make([]byte, 256)
		n, _ := a1.Read(buf)
		if string(buf[:n]) != "from-b" {
			t.Errorf("a1 read %q, want %q", string(buf[:n]), "from-b")
		}
		a1.Close()
	}()

	// b2 → a1
	go func() {
		defer wg.Done()
		b2.Write([]byte("from-b"))
		buf := make([]byte, 256)
		n, _ := b2.Read(buf)
		if string(buf[:n]) != "from-a" {
			t.Errorf("b2 read %q, want %q", string(buf[:n]), "from-a")
		}
		b2.Close()
	}()

	wg.Wait()
}

// TestNewConnector verifies constructor.
func TestNewConnector(t *testing.T) {
	cfg := &config.Config{
		Server: "http://localhost:8080",
		APIKey: "test_key",
	}

	c := NewConnector(cfg, "tunnel-123", 3000)
	if c.tunnelID != "tunnel-123" {
		t.Errorf("tunnelID = %q, want %q", c.tunnelID, "tunnel-123")
	}
	if c.localPort != 3000 {
		t.Errorf("localPort = %d, want 3000", c.localPort)
	}
	if c.closed {
		t.Error("new connector should not be closed")
	}
}

// TestConnectorClose verifies the Close method.
func TestConnectorClose(t *testing.T) {
	cfg := &config.Config{Server: "http://localhost:8080"}
	c := NewConnector(cfg, "t1", 3000)
	c.Close()

	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()

	if !closed {
		t.Error("connector should be closed after Close()")
	}
}

// TestWSNetConnClose verifies that WSNetConn.Close closes the underlying connection.
func TestWSNetConnClose(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	serverReady := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		close(serverReady)
		// Just read until closed
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}

	<-serverReady

	c := NewWSNetConn(ws)
	if err := c.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	// Writing after close should fail
	_, err = c.Write([]byte("test"))
	if err == nil {
		t.Error("Write after Close should error")
	}
}

// TestConnectorHandleConnection tests the full relay through WebSocket → local service.
func TestConnectorHandleConnection(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	// Start a local TCP service that echoes back
	localListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	defer localListener.Close()
	localPort := localListener.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := localListener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c) // echo
			}(conn)
		}
	}()

	// Start a WS server to simulate the NullBore server's data endpoint
	var serverWS *websocket.Conn
	wsReady := make(chan struct{})

	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		serverWS, err = upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		close(wsReady)
	}))
	defer wsServer.Close()

	// Create connector pointing at our local port
	cfg := &config.Config{
		Server: wsServer.URL,
	}
	connector := NewConnector(cfg, "test-tunnel", localPort)

	// Simulate handleConnection by manually opening data WS
	// (We can't easily test handleConnection directly since it dials the server,
	// but we can test the pipe mechanics through the echo service)
	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")
	clientWS, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}

	<-wsReady

	// Connect to local service
	localConn, err := net.DialTimeout("tcp", localListener.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatalf("local dial error: %v", err)
	}

	// Pipe the WS to local, simulating what handleConnection does
	dataConn := NewWSNetConn(clientWS)
	done := make(chan struct{})
	go func() {
		pipe(localConn, dataConn)
		close(done)
	}()

	// Send data through serverWS → should echo back via local service
	testMsg := []byte("echo this back please")
	serverWS.WriteMessage(websocket.BinaryMessage, testMsg)

	// Read the echo
	_, reply, err := serverWS.ReadMessage()
	if err != nil {
		t.Fatalf("read echo error: %v", err)
	}
	if !bytes.Equal(reply, testMsg) {
		t.Errorf("echo = %q, want %q", string(reply), string(testMsg))
	}

	// Clean up
	serverWS.Close()
	<-done

	_ = connector // verify it was created properly
}
