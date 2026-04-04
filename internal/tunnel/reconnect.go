package tunnel

import (
	"fmt"
	"log"
	"time"

	"github.com/nullbore/nullbore-client/internal/client"
	"github.com/nullbore/nullbore-client/internal/config"
	"github.com/nullbore/nullbore-client/internal/debug"
)

// RunWithFullReconnect runs the tunnel with full re-registration on disconnect.
// When the control WebSocket drops, it will:
// 1. Wait with exponential backoff
// 2. Re-create the tunnel via REST API (in case the server restarted)
// 3. Reconnect the control WebSocket
// 4. Resume relaying
func RunWithFullReconnect(
	cfg *config.Config,
	apiClient *client.Client,
	port int,
	name string,
	ttl string,
	connector *Connector,
) error {
	backoff := NewBackoff()

	for {
		// Run until disconnect
		connected, err := connector.runOnce()

		connector.mu.Lock()
		closed := connector.closed
		connector.mu.Unlock()
		if closed {
			return nil
		}

		if connected {
			backoff.Reset()
			if err != nil {
				log.Printf("disconnected: %v", err)
			} else {
				log.Printf("disconnected (clean)")
			}
		} else {
			if err != nil {
				log.Printf("connection failed: %v", err)
			}
		}

		// Wait before reconnecting
		d := backoff.Duration()
		log.Printf("reconnecting in %s...", d)
		time.Sleep(d)

		// Re-create the tunnel (server may have restarted, losing in-memory state).
		// Use the previous slug as the name so the server reclaims the same URL.
		reconnectName := name
		if reconnectName == "" {
			connector.mu.Lock()
			prevSlug := connector.slug
			connector.mu.Unlock()
			if prevSlug != "" {
				reconnectName = prevSlug
			}
		}
		log.Printf("re-registering tunnel...")
		t, err := apiClient.CreateTunnel(port, reconnectName, ttl)
		if err != nil {
			log.Printf("tunnel re-registration failed: %v", err)
			continue // will retry with backoff
		}

		debug.Printf("tunnel re-registered: id=%s slug=%s", t.ID, t.Slug)

		// Update connector with new tunnel ID and slug
		connector.mu.Lock()
		connector.tunnelID = t.ID
		connector.slug = t.Slug
		connector.mu.Unlock()

		// Reconnect control WebSocket
		if err := connector.connect(); err != nil {
			debug.Printf("control reconnect failed: %v", err)
			continue
		}

		publicURL := t.PublicURL
		if publicURL == "" {
			publicURL = fmt.Sprintf("%s/t/%s", cfg.ServerURL(), t.Slug)
		}
		log.Printf("reconnected — forwarding %s → localhost:%d", publicURL, port)
	}
}
