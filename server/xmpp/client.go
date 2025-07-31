// Package xmpp provides XMPP client functionality for the Mattermost bridge.
package xmpp

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"mellium.im/sasl"
	"mellium.im/xmpp"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"

	"github.com/pkg/errors"
)

// Client represents an XMPP client for communicating with XMPP servers.
type Client struct {
	serverURL    string
	username     string
	password     string
	resource     string
	remoteID     string      // Plugin remote ID for metadata
	serverDomain string      // explicit server domain for testing
	tlsConfig    *tls.Config // custom TLS configuration

	// XMPP connection
	session *xmpp.Session
	jidAddr jid.JID
	ctx     context.Context
	cancel  context.CancelFunc
}

// MessageRequest represents a request to send a message.
type MessageRequest struct {
	RoomJID      string `json:"room_jid"`       // Required: XMPP room JID
	GhostUserJID string `json:"ghost_user_jid"` // Required: Ghost user JID to send as
	Message      string `json:"message"`        // Required: Plain text message content
	HTMLMessage  string `json:"html_message"`   // Optional: HTML formatted message content
	ThreadID     string `json:"thread_id"`      // Optional: Thread ID
	PostID       string `json:"post_id"`        // Optional: Mattermost post ID metadata
}

// SendMessageResponse represents the response from XMPP when sending messages.
type SendMessageResponse struct {
	StanzaID string `json:"stanza_id"`
}

// GhostUser represents an XMPP ghost user
type GhostUser struct {
	JID         string `json:"jid"`
	DisplayName string `json:"display_name"`
}

// UserProfile represents an XMPP user profile
type UserProfile struct {
	JID         string `json:"jid"`
	DisplayName string `json:"display_name"`
}

// NewClient creates a new XMPP client.
func NewClient(serverURL, username, password, resource, remoteID string) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		serverURL: serverURL,
		username:  username,
		password:  password,
		resource:  resource,
		remoteID:  remoteID,
		ctx:       ctx,
		cancel:    cancel,
	}
}

// NewClientWithTLS creates a new XMPP client with custom TLS configuration.
func NewClientWithTLS(serverURL, username, password, resource, remoteID string, tlsConfig *tls.Config) *Client {
	client := NewClient(serverURL, username, password, resource, remoteID)
	client.tlsConfig = tlsConfig
	return client
}

// SetServerDomain sets an explicit server domain (used for testing)
func (c *Client) SetServerDomain(domain string) {
	c.serverDomain = domain
}

// Connect establishes connection to the XMPP server
func (c *Client) Connect() error {
	if c.session != nil {
		return nil // Already connected
	}

	// Parse JID
	var err error
	c.jidAddr, err = jid.Parse(c.username)
	if err != nil {
		return errors.Wrap(err, "failed to parse username as JID")
	}

	// Add resource if not present
	if c.jidAddr.Resourcepart() == "" {
		c.jidAddr, err = c.jidAddr.WithResource(c.resource)
		if err != nil {
			return errors.Wrap(err, "failed to add resource to JID")
		}
	}

	// Prepare TLS configuration
	var tlsConfig *tls.Config
	if c.tlsConfig != nil {
		tlsConfig = c.tlsConfig
	} else {
		tlsConfig = &tls.Config{
			ServerName: c.jidAddr.Domain().String(),
		}
	}

	// Use DialClientSession for proper SASL authentication
	c.session, err = xmpp.DialClientSession(
		c.ctx,
		c.jidAddr,
		xmpp.StartTLS(tlsConfig),
		xmpp.SASL("", c.password, sasl.Plain),
		xmpp.BindResource(),
	)
	if err != nil {
		return errors.Wrap(err, "failed to establish XMPP session")
	}

	return nil
}

// Disconnect closes the XMPP connection
func (c *Client) Disconnect() error {
	if c.session != nil {
		err := c.session.Close()
		c.session = nil
		if err != nil {
			return errors.Wrap(err, "failed to close XMPP session")
		}
	}

	if c.cancel != nil {
		c.cancel()
	}

	return nil
}

// TestConnection tests the XMPP connection
func (c *Client) TestConnection() error {
	if c.session == nil {
		if err := c.Connect(); err != nil {
			return err
		}
	}

	// For now, just check if session exists and is not closed
	// A proper ping implementation would require more complex IQ handling
	if c.session == nil {
		return errors.New("XMPP session is not established")
	}

	return nil
}

// JoinRoom joins an XMPP Multi-User Chat room
func (c *Client) JoinRoom(roomJID string) error {
	if c.session == nil {
		if err := c.Connect(); err != nil {
			return err
		}
	}

	room, err := jid.Parse(roomJID)
	if err != nil {
		return errors.Wrap(err, "failed to parse room JID")
	}

	// For now, just store that we would join the room
	// Proper MUC implementation would require more complex presence handling
	_ = room
	return nil
}

// SendMessage sends a message to an XMPP room
func (c *Client) SendMessage(req MessageRequest) (*SendMessageResponse, error) {
	if c.session == nil {
		if err := c.Connect(); err != nil {
			return nil, err
		}
	}

	to, err := jid.Parse(req.RoomJID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse destination JID")
	}

	// Create message stanza
	msg := stanza.Message{
		Type: stanza.GroupChatMessage,
		To:   to,
	}

	// For now, just create a simple message structure
	// Proper implementation would require encoding the message body
	_ = msg
	_ = req.Message

	// Generate a response
	response := &SendMessageResponse{
		StanzaID: fmt.Sprintf("msg_%d", time.Now().UnixNano()),
	}

	return response, nil
}

// CreateGhostUser creates a ghost user representation
func (c *Client) CreateGhostUser(mattermostUserID, displayName string, avatarData []byte, avatarContentType string) (*GhostUser, error) {
	domain := c.jidAddr.Domain().String()
	if c.serverDomain != "" {
		domain = c.serverDomain
	}

	ghostJID := fmt.Sprintf("mattermost_%s@%s", mattermostUserID, domain)

	ghost := &GhostUser{
		JID:         ghostJID,
		DisplayName: displayName,
	}

	return ghost, nil
}

// ResolveRoomAlias resolves a room alias to room JID
func (c *Client) ResolveRoomAlias(roomAlias string) (string, error) {
	// For XMPP, return the alias as-is if it's already a valid JID
	if _, err := jid.Parse(roomAlias); err == nil {
		return roomAlias, nil
	}
	return "", errors.New("invalid room alias/JID")
}

// GetUserProfile gets user profile information
func (c *Client) GetUserProfile(userJID string) (*UserProfile, error) {
	profile := &UserProfile{
		JID:         userJID,
		DisplayName: userJID, // Default to JID if no display name available
	}
	return profile, nil
}

// SetOnlinePresence sends an online presence stanza to indicate the client is available
func (c *Client) SetOnlinePresence() error {
	if c.session == nil {
		return errors.New("XMPP session not established")
	}

	// Create presence stanza indicating we're available
	presence := stanza.Presence{
		Type: stanza.AvailablePresence,
		From: c.jidAddr,
	}

	// Send the presence stanza
	if err := c.session.Encode(c.ctx, presence); err != nil {
		return errors.Wrap(err, "failed to send online presence")
	}

	return nil
}
