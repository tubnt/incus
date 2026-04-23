package cluster

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Event is one row from Incus' /1.0/events stream. Metadata is kept as
// RawMessage because the shape varies by Type (lifecycle vs logging vs
// operation); callers parse the inner payload for the types they actually
// handle and ignore the rest.
type Event struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Location  string          `json:"location,omitempty"`
	Project   string          `json:"project,omitempty"`
	Metadata  json.RawMessage `json:"metadata"`
}

// LifecycleMetadata is the minimal subset of metadata for type=lifecycle
// events. `Source` is an Incus API path like /1.0/instances/vm-xxx from
// which we extract the instance name. `Action` tells us what changed.
type LifecycleMetadata struct {
	Action  string          `json:"action"`
	Source  string          `json:"source"`
	Context json.RawMessage `json:"context,omitempty"`
}

// ClusterMemberMetadata covers type=cluster events — used by the HA-aware
// worker to detect node transitions (online ⇄ offline).
type ClusterMemberMetadata struct {
	Action  string          `json:"action"`
	Source  string          `json:"source"`
	Context json.RawMessage `json:"context,omitempty"`
}

// StreamEvents dials wss://<apiURL>/1.0/events?type=<types...> using the
// cluster's pinned TLS config and forwards parsed Event records to handler
// until ctx cancels or the connection drops. Returns the error that
// terminated the stream; a context-cancel returns nil for graceful shutdown.
//
// Kept as a package-level function (not a Client method) so the worker can
// drive its own reconnect loop with Manager-backed TLS without widening the
// Client surface.
func StreamEvents(ctx context.Context, tlsCfg *tls.Config, apiURL string, types []string, handler func(Event) error) error {
	wsURL, err := buildEventsWSURL(apiURL, types)
	if err != nil {
		return err
	}

	// Incus serves both HTTP/1.1 and HTTP/2; WebSocket needs HTTP/1.1 (RFC
	// 8441 h2 Extended Connect isn't implemented in Incus). Force the TLS
	// ALPN to http/1.1 so the handshake doesn't fail with "bad handshake"
	// after h2 gets selected.
	if tlsCfg != nil {
		tlsCfg = tlsCfg.Clone()
		tlsCfg.NextProtos = []string{"http/1.1"}
	}

	dialer := websocket.Dialer{
		TLSClientConfig:  tlsCfg,
		HandshakeTimeout: 10 * time.Second,
	}

	conn, resp, err := dialer.DialContext(ctx, wsURL, http.Header{})
	if err != nil {
		// Surface the HTTP response (if any) so operators can tell WS-specific
		// handshake failures apart from TLS / network issues. gorilla returns
		// bad-handshake along with the response when the server rejected the
		// upgrade, and nil response for lower-level failures.
		if resp != nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			slog.Warn("ws dial rejected",
				"url", wsURL,
				"status", resp.StatusCode,
				"proto", resp.Proto,
				"content_type", resp.Header.Get("Content-Type"),
				"body", strings.TrimSpace(string(body)),
			)
		}
		return fmt.Errorf("ws dial %s: %w", wsURL, err)
	}
	defer func() { _ = conn.Close() }()

	// Close the socket when ctx cancels so the blocking ReadMessage returns.
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("ws read: %w", err)
		}
		var ev Event
		if uerr := json.Unmarshal(data, &ev); uerr != nil {
			// Skip malformed frames — a single bad event shouldn't terminate
			// the subscription. The reconciler is the safety net.
			continue
		}
		if herr := handler(ev); herr != nil {
			return herr
		}
	}
}

// InstanceNameFromSource extracts "vm-xxx" from "/1.0/instances/vm-xxx" etc.
// Returns empty when the source path doesn't match the expected shape (e.g.
// pool / network events), letting callers short-circuit non-instance events.
func InstanceNameFromSource(source string) string {
	const prefix = "/1.0/instances/"
	if !strings.HasPrefix(source, prefix) {
		return ""
	}
	rest := source[len(prefix):]
	// Strip trailing subpaths like /state, /snapshots/xxx
	if idx := strings.IndexByte(rest, '/'); idx >= 0 {
		return rest[:idx]
	}
	return rest
}

// ClusterMemberNameFromSource extracts "nodeX" from "/1.0/cluster/members/nodeX".
func ClusterMemberNameFromSource(source string) string {
	const prefix = "/1.0/cluster/members/"
	if !strings.HasPrefix(source, prefix) {
		return ""
	}
	rest := source[len(prefix):]
	if idx := strings.IndexByte(rest, '/'); idx >= 0 {
		return rest[:idx]
	}
	return rest
}

func buildEventsWSURL(apiURL string, types []string) (string, error) {
	u, err := url.Parse(apiURL)
	if err != nil {
		return "", fmt.Errorf("parse api url: %w", err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	u.Path = "/1.0/events"
	if len(types) > 0 {
		q := u.Query()
		q.Set("type", strings.Join(types, ","))
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}
