package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/nullbore/nullbore-client/internal/config"
)

// Client communicates with the NullBore server REST API.
type Client struct {
	cfg    *config.Config
	http   *http.Client
}

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
}

func New(cfg *config.Config) *Client {
	return &Client{
		cfg: cfg,
		http: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// CreateTunnel registers a new tunnel with the server.
func (c *Client) CreateTunnel(port int, name, ttl string) (*Tunnel, error) {
	body := map[string]interface{}{
		"local_port": port,
	}
	if name != "" {
		body["name"] = name
	}
	if ttl != "" {
		body["ttl"] = ttl
	}

	var t Tunnel
	if err := c.post("/v1/tunnels", body, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

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
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", c.cfg.ServerURL()+path, bytes.NewReader(data))
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

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("server error (%d): %s", resp.StatusCode, string(respBody))
	}

	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("parsing response: %w", err)
		}
	}

	return nil
}
