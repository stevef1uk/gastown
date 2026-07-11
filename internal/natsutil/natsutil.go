package natsutil

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/nats-io/nats.go"
)

// DefaultURL is the default NATS connection URL.
const DefaultURL = "nats://127.0.0.1:4222"

// connectOptions returns the shared reconnect/keepalive options used by all
// Gas Town NATS clients. The exitOnClose flag is only safe for standalone
// processes that have a supervisor (daemon) to restart them.
func connectOptions(name string, exitOnClose bool) []nats.Option {
	opts := []nats.Option{
		nats.Name(name),
		nats.Timeout(5 * time.Second),
		nats.ReconnectWait(1 * time.Second),
		nats.MaxReconnects(-1), // unlimited
		nats.PingInterval(30 * time.Second),
		nats.MaxPingsOutstanding(3),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			if err != nil {
				log.Printf("[nats:%s] disconnected: %v", name, err)
			} else {
				log.Printf("[nats:%s] disconnected", name)
			}
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			log.Printf("[nats:%s] reconnected to %s", name, nc.ConnectedUrl())
		}),
	}
	if exitOnClose {
		opts = append(opts, nats.ClosedHandler(func(nc *nats.Conn) {
			log.Printf("[nats:%s] connection closed permanently", name)
			// Standalone services should be restarted by their supervisor.
			// Exiting here ensures a dead connection is never silently ignored.
			if os.Getenv("GT_NATS_EXIT_ON_CLOSE") != "0" {
				os.Exit(1)
			}
		}))
	}
	return opts
}

// ConnectRobust opens a NATS connection with aggressive reconnect behavior.
// Use this for clients that run inside another process (daemon, web server).
func ConnectRobust(url, name string) (*nats.Conn, error) {
	if url == "" {
		url = DefaultURL
	}
	return nats.Connect(url, connectOptions(name, false)...)
}

// ConnectRobustService opens a NATS connection with aggressive reconnect behavior
// and exits the process if the connection is permanently closed. Use this only
// for standalone processes that have a supervisor to restart them.
func ConnectRobustService(url, name string) (*nats.Conn, error) {
	if url == "" {
		url = DefaultURL
	}
	return nats.Connect(url, connectOptions(name, true)...)
}

// ActivityEvent represents a real-time agent activity update.
type ActivityEvent struct {
	RigID     string    `json:"rig_id"`
	AgentID   string    `json:"agent_id"`
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	Payload   string    `json:"payload,omitempty"`
}

// Client is a wrapper around a NATS connection.
type Client struct {
	nc *nats.Conn
}

// NewClient creates a new NATS client.
func NewClient(url string) (*Client, error) {
	if url == "" {
		url = DefaultURL
	}
	nc, err := nats.Connect(url, nats.Timeout(2*time.Second))
	if err != nil {
		return nil, err
	}
	return &Client{nc: nc}, nil
}

// Close closes the NATS connection.
func (c *Client) Close() {
	if c.nc != nil {
		c.nc.Close()
	}
}

// PublishActivity sends an activity update to NATS.
func (c *Client) PublishActivity(event ActivityEvent) error {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	subject := fmt.Sprintf("gt.activity.%s.%s", event.RigID, event.AgentID)
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return c.nc.Publish(subject, data)
}

// SubscribeToActivity subscribes to all activity updates.
func (c *Client) SubscribeToActivity(callback func(ActivityEvent)) (*nats.Subscription, error) {
	return c.nc.Subscribe("gt.activity.*.*", func(m *nats.Msg) {
		var event ActivityEvent
		if err := json.Unmarshal(m.Data, &event); err == nil {
			callback(event)
		}
	})
}
