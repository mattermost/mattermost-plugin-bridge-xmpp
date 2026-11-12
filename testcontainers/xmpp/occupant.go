package xmpp

import (
	"crypto/tls"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"mellium.im/xmlstream"
	"mellium.im/xmpp/stanza"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/logger"
	xmppclient "github.com/mattermost/mattermost-plugin-bridge-xmpp/server/xmpp"
)

// ObservedMessage is a MUC groupchat body captured by an OccupantClient.
type ObservedMessage struct {
	From string
	Body string
	At   time.Time
}

// OccupantClient is a test XMPP client that can join rooms, send groupchat, and observe MUC traffic.
type OccupantClient struct {
	client *xmppclient.Client
	log    logger.Logger

	mu       sync.Mutex
	messages []ObservedMessage
	notify   chan struct{}
}

// ConnectOccupant connects an occupant account to the Prosody container.
func ConnectOccupant(t testing.TB, container *Container, resource string, log logger.Logger) *OccupantClient {
	t.Helper()

	oc := &OccupantClient{
		log:    log,
		notify: make(chan struct{}, 1),
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // local Prosody testcontainer
	}
	client := xmppclient.NewClientWithTLS(
		container.HostURL,
		container.Config.OccupantJID,
		container.Config.OccupantPassword,
		resource,
		"test-remote",
		tlsConfig,
		log,
	)
	client.SetMessageHandler(oc.handleMessage)
	require.NoError(t, client.Connect())
	oc.client = client

	t.Cleanup(func() {
		_ = oc.Close()
	})

	return oc
}

func (c *OccupantClient) handleMessage(msg stanza.Message, tokens xmlstream.TokenReadEncoder) error {
	if msg.Type != stanza.GroupChatMessage {
		return nil
	}
	body, err := c.client.ExtractMessageBody(tokens)
	if err != nil {
		c.log.LogWarn("failed to extract message body", "error", err)
		return nil
	}
	if strings.TrimSpace(body) == "" {
		return nil
	}

	c.mu.Lock()
	c.messages = append(c.messages, ObservedMessage{
		From: msg.From.String(),
		Body: body,
		At:   time.Now(),
	})
	c.mu.Unlock()

	select {
	case c.notify <- struct{}{}:
	default:
	}
	return nil
}

// JoinRoom joins a MUC room (creating it when Prosody allows).
func (c *OccupantClient) JoinRoom(roomJID string) error {
	return c.client.JoinRoom(roomJID)
}

// SendGroupchat sends a groupchat message to a joined room.
func (c *OccupantClient) SendGroupchat(roomJID, body string) error {
	_, err := c.client.SendMessage(&xmppclient.MessageRequest{
		RoomJID: roomJID,
		Message: body,
	})
	return err
}

// WaitForMessage waits until an observed MUC message contains token.
func (c *OccupantClient) WaitForMessage(token string, timeout time.Duration) (ObservedMessage, error) {
	deadline := time.Now().Add(timeout)
	for {
		if msg, ok := c.findMessage(token); ok {
			return msg, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			c.mu.Lock()
			snapshot := append([]ObservedMessage(nil), c.messages...)
			c.mu.Unlock()
			return ObservedMessage{}, fmt.Errorf("xmpp message containing %q not seen within %s; observed=%v", token, timeout, snapshot)
		}
		timer := time.NewTimer(remaining)
		select {
		case <-c.notify:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// AssertNoMessage ensures no message containing token arrives within waitFor.
func (c *OccupantClient) AssertNoMessage(token string, waitFor time.Duration) error {
	deadline := time.Now().Add(waitFor)
	for time.Now().Before(deadline) {
		if _, ok := c.findMessage(token); ok {
			return fmt.Errorf("unexpected xmpp message containing %q", token)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil
}

func (c *OccupantClient) findMessage(token string) (ObservedMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, msg := range c.messages {
		if strings.Contains(msg.Body, token) {
			return msg, true
		}
	}
	return ObservedMessage{}, false
}

// ClearMessages drops observed messages between assertions.
func (c *OccupantClient) ClearMessages() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = nil
}

// Close disconnects the occupant client.
func (c *OccupantClient) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Disconnect()
}
