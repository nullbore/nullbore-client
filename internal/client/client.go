package client

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nullbore/nullbore-client/internal/config"
)

// SupportedAPIVersion is the server API major version this client speaks. It
// matches the URL path prefix used for requests (/v1). The server advertises
// its own version via the X-NullBore-API response header; if the server reports
// a newer major version, the client warns the user to upgrade.
const SupportedAPIVersion = "1"

// Version is the client build version, set by the CLI at startup (ldflags-driven).
// Used in the User-Agent so the server can observe which client versions are in
// the field — important for planning deprecations.
var Version = "dev"

// apiCompatWarning returns an upgrade warning if the server's advertised API
// major version is newer than this client supports. Empty = compatible (equal,
// older, or unparseable/unknown — never warn spuriously).
func apiCompatWarning(serverAPI string) string {
	if serverAPI == "" || serverAPI == SupportedAPIVersion {
		return ""
	}
	sv, err1 := strconv.Atoi(serverAPI)
	cv, err2 := strconv.Atoi(SupportedAPIVersion)
	if err1 != nil || err2 != nil || sv <= cv {
		return ""
	}
	return fmt.Sprintf("This NullBore server uses API v%s, but your client speaks v%s. "+
		"Some features may not work — update with: nullbore update", serverAPI, SupportedAPIVersion)
}

// APIError is a non-2xx response from the server. Its Error() text keeps the
// historical "server error (CODE): BODY" format.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("server error (%d): %s", e.StatusCode, e.Body)
}

// Message returns the server's human-readable error: the "error" field of a
// JSON body if present, otherwise the trimmed raw body.
func (e *APIError) Message() string {
	var v struct {
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(e.Body), &v) == nil && v.Error != "" {
		return v.Error
	}
	return strings.TrimSpace(e.Body)
}

// Client communicates with the NullBore server REST API.
type Client struct {
	cfg      *config.Config
	http     *http.Client
	takeover bool
}

// SetTakeover enables/disables the device takeover flag for the next request.
func (c *Client) SetTakeover(v bool) { c.takeover = v }

// Tunnel represents a tunnel from the API.
type Tunnel struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	ClientID  string `json:"client_id"`
	LocalPort int    `json:"local_port"`
	Name      string `json:"name,omitempty"`
	TTL       string `json:"ttl"`
	Mode      string `json:"mode"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
	BytesIn   int64  `json:"bytes_in"`
	BytesOut  int64  `json:"bytes_out"`
	Requests  int64  `json:"requests"`
	PublicURL string `json:"public_url,omitempty"`
}

func New(cfg *config.Config) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.InsecureSkipVerify() {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	return &Client{
		cfg: cfg,
		http: &http.Client{
			Timeout:   15 * time.Second,
			Transport: transport,
		},
	}
}

// Tunnel modes accepted by POST /v1/tunnels (the "mode" field).
const (
	// ModeRelay is the default: the relay terminates TLS and proxies HTTP.
	// Never sent explicitly — an omitted mode means relay.
	ModeRelay = "relay"
	// ModeTLSPassthrough makes the relay forward raw TLS bytes end-to-end.
	// The local service must speak TLS itself; the relay cannot inspect
	// traffic or add basic auth. Paid plans only.
	ModeTLSPassthrough = "tls-passthrough"
)

// NormalizeMode validates a user-supplied tunnel mode and returns the value to
// send to the server. "" and "relay" both normalize to "" (omit the field, so
// requests are byte-identical to pre-mode clients).
func NormalizeMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", ModeRelay:
		return "", nil
	case ModeTLSPassthrough:
		return ModeTLSPassthrough, nil
	default:
		return "", fmt.Errorf("unknown tunnel mode %q (valid: %s, %s)", mode, ModeRelay, ModeTLSPassthrough)
	}
}

// IsTLSPassthrough reports whether a tunnel mode is end-to-end TLS passthrough.
func IsTLSPassthrough(mode string) bool { return mode == ModeTLSPassthrough }

// TunnelOptions holds every option accepted when creating a tunnel.
type TunnelOptions struct {
	Port     int
	Name     string
	TTL      string
	Source   string // "cli" or "daemon"
	AuthUser string
	AuthPass string
	Mode     string // "" (relay) or ModeTLSPassthrough
}

// Validate rejects option combinations the server would refuse anyway, so the
// user gets a clear local error instead of a round trip.
func (o TunnelOptions) Validate() error {
	if _, err := NormalizeMode(o.Mode); err != nil {
		return err
	}
	if IsTLSPassthrough(o.Mode) && (o.AuthUser != "" || o.AuthPass != "") {
		return fmt.Errorf("basic auth cannot be combined with TLS passthrough: the relay never sees decrypted traffic, so it cannot enforce auth (add auth in your local service instead)")
	}
	return nil
}

// createTunnelBody builds the JSON body for POST /v1/tunnels. Optional fields
// (name, ttl, auth, mode) are included only when set.
func createTunnelBody(o TunnelOptions, deviceName string) map[string]interface{} {
	body := map[string]interface{}{
		"local_port":  o.Port,
		"device_name": deviceName,
		"source":      o.Source,
	}
	if o.Name != "" {
		body["name"] = o.Name
	}
	if o.TTL != "" {
		body["ttl"] = o.TTL
	}
	if o.AuthUser != "" && o.AuthPass != "" {
		body["auth_user"] = o.AuthUser
		body["auth_pass"] = o.AuthPass
	}
	if o.Mode != "" && o.Mode != ModeRelay {
		body["mode"] = o.Mode
	}
	return body
}

// CreateTunnel registers a new tunnel with the server.
func (c *Client) CreateTunnel(port int, name, ttl string) (*Tunnel, error) {
	return c.CreateTunnelFull(port, name, ttl, "cli", "", "")
}

// CreateTunnelWithSource registers a tunnel, tagging it with a source ("cli" or "daemon").
func (c *Client) CreateTunnelWithSource(port int, name, ttl, source string) (*Tunnel, error) {
	return c.CreateTunnelFull(port, name, ttl, source, "", "")
}

// CreateTunnelFull registers a tunnel with all options including basic auth.
func (c *Client) CreateTunnelFull(port int, name, ttl, source, authUser, authPass string) (*Tunnel, error) {
	return c.CreateTunnelWithOptions(TunnelOptions{
		Port: port, Name: name, TTL: ttl, Source: source,
		AuthUser: authUser, AuthPass: authPass,
	})
}

// CreateTunnelWithOptions registers a tunnel with the full option set,
// including the tunnel mode.
func (c *Client) CreateTunnelWithOptions(o TunnelOptions) (*Tunnel, error) {
	mode, err := NormalizeMode(o.Mode)
	if err != nil {
		return nil, err
	}
	o.Mode = mode
	if err := o.Validate(); err != nil {
		return nil, err
	}

	deviceName := c.cfg.DeviceName
	if deviceName == "" {
		deviceName, _ = os.Hostname()
	}

	var t Tunnel
	if err := c.post("/v1/tunnels", createTunnelBody(o, deviceName), &t); err != nil {
		return nil, explainModeError(err, o.Mode)
	}

	// A server that predates TLS passthrough ignores the unknown field and
	// creates a plain relay tunnel. That tunnel would silently break (the relay
	// would speak HTTP to a TLS-only service), so refuse it and clean up.
	if IsTLSPassthrough(o.Mode) && t.Mode != ModeTLSPassthrough {
		if t.ID != "" {
			_ = c.CloseTunnel(t.ID)
		}
		got := t.Mode
		if got == "" {
			got = "unspecified"
		}
		return nil, fmt.Errorf("server does not support TLS passthrough (it created a %q tunnel instead; closed it) — the server needs upgrading", got)
	}
	return &t, nil
}

// explainModeError adds context to server rejections of a TLS passthrough
// request. The server answers 403 for plans without passthrough and 400 for
// invalid combinations (e.g. basic auth).
func explainModeError(err error, mode string) error {
	if !IsTLSPassthrough(mode) {
		return err
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch apiErr.StatusCode {
	case http.StatusForbidden:
		return &modeError{
			msg: fmt.Sprintf("TLS passthrough is not available on your plan — it requires a paid plan (server said: %s)", apiErr.Message()),
			err: err,
		}
	case http.StatusBadRequest:
		return &modeError{
			msg: fmt.Sprintf("server rejected the TLS passthrough tunnel (server said: %s)", apiErr.Message()),
			err: err,
		}
	}
	return err
}

// modeError replaces the raw server error text with a clearer message while
// keeping the underlying *APIError reachable via errors.As.
type modeError struct {
	msg string
	err error
}

func (e *modeError) Error() string { return e.msg }
func (e *modeError) Unwrap() error { return e.err }

// ListTunnels returns all active tunnels.
func (c *Client) ListTunnels() ([]Tunnel, error) {
	var tunnels []Tunnel
	if err := c.get("/v1/tunnels", &tunnels); err != nil {
		return nil, err
	}
	return tunnels, nil
}

// CloseTunnel closes a tunnel by ID.
func (c *Client) CloseTunnel(id string) error {
	return c.del("/v1/tunnels/" + id)
}

// RequestLog represents a logged request from the server.
type RequestLog struct {
	ID        string `json:"id"`
	TunnelID  string `json:"tunnel_id"`
	Slug      string `json:"slug"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	Headers   string `json:"headers"`
	BodySize  int64  `json:"body_size"`
	BodySnip  string `json:"body_snippet,omitempty"`
	RemoteIP  string `json:"remote_ip"`
	CreatedAt string `json:"created_at"`
}

// ListRequests returns recent request logs for a tunnel.
func (c *Client) ListRequests(tunnelID string, limit int) ([]RequestLog, error) {
	path := fmt.Sprintf("/v1/tunnels/%s/requests?limit=%d", tunnelID, limit)
	var logs []RequestLog
	if err := c.get(path, &logs); err != nil {
		return nil, err
	}
	return logs, nil
}

// Health checks the server status.
func (c *Client) Health() (map[string]string, error) {
	var result map[string]string
	if err := c.get("/health", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// --- HTTP helpers ---

func (c *Client) get(path string, out interface{}) error {
	req, err := http.NewRequest("GET", c.cfg.ServerURL()+path, nil)
	if err != nil {
		return err
	}
	return c.do(req, out)
}

func (c *Client) post(path string, body interface{}, out interface{}) error {
	return c.send("POST", path, body, out)
}

// send issues a request with a JSON body (any method, e.g. DELETE with a body).
func (c *Client) send(method, path string, body interface{}, out interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(method, c.cfg.ServerURL()+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, out)
}

func (c *Client) del(path string) error {
	req, err := http.NewRequest("DELETE", c.cfg.ServerURL()+path, nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

func (c *Client) do(req *http.Request, out interface{}) error {
	if token := c.cfg.Token(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	// Send device identity headers
	if c.cfg.DeviceID != "" {
		req.Header.Set("X-NullBore-Device-ID", c.cfg.DeviceID)
	}
	if c.cfg.DeviceName != "" {
		req.Header.Set("X-NullBore-Device-Hostname", c.cfg.DeviceName)
	} else if hostname, _ := os.Hostname(); hostname != "" {
		req.Header.Set("X-NullBore-Device-Hostname", hostname)
	}
	if c.takeover {
		req.Header.Set("X-NullBore-Device-Takeover", "true")
	}
	req.Header.Set("User-Agent", "nullbore-client/"+Version+" (api/"+SupportedAPIVersion+")")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check for device warning
	if warning := resp.Header.Get("X-NullBore-Device-Warning"); warning != "" {
		fmt.Fprintf(os.Stderr, "\n⚠️  %s\n\n", warning)
	}

	// Warn if the server's API major version is newer than this client supports.
	if warning := apiCompatWarning(resp.Header.Get("X-NullBore-API")); warning != "" {
		fmt.Fprintf(os.Stderr, "\n⚠️  %s\n\n", warning)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return &APIError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("parsing response: %w", err)
		}
	}

	return nil
}
