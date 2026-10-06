package client

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// acmeDNS01Path is the server's DNS-01 delegation endpoint. POST creates a
// TXT record, DELETE removes it; both take an ACMEDNS01Request body.
const acmeDNS01Path = "/v1/acme/dns-01"

// ACMEDNS01Request is the body of POST/DELETE /v1/acme/dns-01.
type ACMEDNS01Request struct {
	FQDN  string `json:"fqdn"`
	Value string `json:"value"`
}

// ACMEDNS01Record is the server's answer to a successful present.
type ACMEDNS01Record struct {
	FQDN      string `json:"fqdn"`
	Value     string `json:"value"`
	ExpiresAt string `json:"expires_at"`
}

// NormalizeFQDN trims whitespace, strips one trailing dot (lego passes
// "_acme-challenge.example.com.") and lowercases the name. The server does the
// same; doing it client-side keeps messages and DNS lookups clean.
func NormalizeFQDN(fqdn string) string {
	fqdn = strings.TrimSpace(fqdn)
	fqdn = strings.TrimSuffix(fqdn, ".")
	return strings.ToLower(fqdn)
}

// PresentDNS01 asks the server to publish a TXT record for an ACME DNS-01
// challenge. Only the challenge value leaves this machine — never a key.
func (c *Client) PresentDNS01(fqdn, value string) (*ACMEDNS01Record, error) {
	var rec ACMEDNS01Record
	body := ACMEDNS01Request{FQDN: NormalizeFQDN(fqdn), Value: value}
	if err := c.send(http.MethodPost, acmeDNS01Path, body, &rec); err != nil {
		return nil, explainACMEError(err)
	}
	return &rec, nil
}

// CleanupDNS01 asks the server to remove a challenge TXT record. It reports
// whether a record was actually deleted; a missing record is not an error.
func (c *Client) CleanupDNS01(fqdn, value string) (bool, error) {
	var res struct {
		Deleted bool `json:"deleted"`
	}
	body := ACMEDNS01Request{FQDN: NormalizeFQDN(fqdn), Value: value}
	if err := c.send(http.MethodDelete, acmeDNS01Path, body, &res); err != nil {
		return false, explainACMEError(err)
	}
	return res.Deleted, nil
}

// explainACMEError turns DNS-01 API failures into actionable messages while
// keeping the *APIError reachable via errors.As.
func explainACMEError(err error) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	msg := apiErr.Message()
	var text string
	switch apiErr.StatusCode {
	case http.StatusBadRequest:
		text = fmt.Sprintf("invalid DNS-01 request: %s", msg)
	case http.StatusUnauthorized:
		text = fmt.Sprintf("API key rejected (%s) — check NULLBORE_API_KEY or api_key in config.toml", msg)
	case http.StatusForbidden:
		text = fmt.Sprintf("DNS-01 record refused: %s — the name must be _acme-challenge.<name>.<your-account>.e2e.nullbore.com "+
			"(or _acme-challenge.<your-account>.e2e.nullbore.com for the wildcard), and certificate delegation requires a paid plan", msg)
	case http.StatusNotFound, http.StatusNotImplemented:
		text = fmt.Sprintf("this NullBore server does not support ACME DNS-01 delegation (%s) — "+
			"it may need upgrading, or (self-hosted) has no DNS provider configured", msg)
	case http.StatusTooManyRequests:
		text = fmt.Sprintf("rate limited by the server (%s) — wait a minute and retry; avoid re-running the ACME client in a tight loop", msg)
	case http.StatusBadGateway:
		text = fmt.Sprintf("the server could not update DNS (%s) — this is usually transient, retry shortly", msg)
	default:
		return err
	}
	return &acmeError{msg: text, err: err}
}

// acmeError replaces the raw server error text with a clearer message.
type acmeError struct {
	msg string
	err error
}

func (e *acmeError) Error() string { return e.msg }
func (e *acmeError) Unwrap() error { return e.err }
