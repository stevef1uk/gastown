package natsutil

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

// DefaultURL is the default NATS connection URL.
const DefaultURL = "nats://127.0.0.1:4222"

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
