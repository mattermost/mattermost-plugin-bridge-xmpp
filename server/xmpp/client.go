// Package xmpp provides XMPP client functionality for the Mattermost bridge.
package xmpp

import (
	"context"
	"crypto/tls"
	"encoding/xml"
	"fmt"
	"time"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/logger"
	"mellium.im/sasl"
	"mellium.im/xmpp"
	"mellium.im/xmpp/disco"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/muc"
	"mellium.im/xmpp/mux"
	"mellium.im/xmpp/stanza"
)

const (
	// defaultOperationTimeout is the default timeout for XMPP operations
	defaultOperationTimeout = 5 * time.Second
)

// Client represents an XMPP client for communicating with XMPP servers.
type Client struct {
	serverURL    string
	username     string
	password     string
	resource     string
	remoteID     string        // Plugin remote ID for metadata
	serverDomain string        // explicit server domain for testing
	tlsConfig    *tls.Config   // custom TLS configuration
	logger       logger.Logger // Logger for debugging

	// XMPP connection
	session        *xmpp.Session
	jidAddr        jid.JID
	ctx            context.Context
	cancel         context.CancelFunc
	mucClient      *muc.Client
	mux            *mux.ServeMux
	sessionReady   chan struct{}
	sessionServing bool
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

// MessageBody represents the body element of an XMPP message
type MessageBody struct {
	XMLName xml.Name `xml:"body"`
	Text    string   `xml:",chardata"`
}

// XMPPMessage represents a complete XMPP message stanza
type XMPPMessage struct {
	XMLName xml.Name    `xml:"jabber:client message"`
	Type    string      `xml:"type,attr"`
	To      string      `xml:"to,attr"`
	From    string      `xml:"from,attr"`
	Body    MessageBody `xml:"body"`
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
func NewClient(serverURL, username, password, resource, remoteID string, logger logger.Logger) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	mucClient := &muc.Client{}
	mux := mux.New("jabber:client", muc.HandleClient(mucClient))

	return &Client{
		serverURL:    serverURL,
		username:     username,
		password:     password,
		resource:     resource,
		remoteID:     remoteID,
		logger:       logger,
		ctx:          ctx,
		cancel:       cancel,
		mucClient:    mucClient,
		mux:          mux,
		sessionReady: make(chan struct{}),
	}
}

// NewClientWithTLS creates a new XMPP client with custom TLS configuration.
func NewClientWithTLS(serverURL, username, password, resource, remoteID string, tlsConfig *tls.Config, logger logger.Logger) *Client {
	client := NewClient(serverURL, username, password, resource, remoteID, logger)
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

	// Reset session ready channel for reconnection
	c.sessionReady = make(chan struct{})
	c.sessionServing = false

	// Parse JID
	var err error
	c.jidAddr, err = jid.Parse(c.username)
	if err != nil {
		return fmt.Errorf("failed to parse username as JID: %w", err)
	}

	// Add resource if not present
	if c.jidAddr.Resourcepart() == "" {
		c.jidAddr, err = c.jidAddr.WithResource(c.resource)
		if err != nil {
			return fmt.Errorf("failed to add resource to JID: %w", err)
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

	// Create a timeout context for the connection attempt (30 seconds)
	connectCtx, connectCancel := context.WithTimeout(c.ctx, 30*time.Second)
	defer connectCancel()

	// Use DialClientSession for proper SASL authentication with timeout
	c.session, err = xmpp.DialClientSession(
		connectCtx,
		c.jidAddr,
		xmpp.StartTLS(tlsConfig),
		xmpp.SASL("", c.password, sasl.Plain),
		xmpp.BindResource(),
	)
	if err != nil {
		return fmt.Errorf("failed to establish XMPP session: %w", err)
	}

	// Start serving the session with the multiplexer to handle incoming stanzas
	go c.serveSession()

	// Wait for the session to be ready with a timeout
	select {
	case <-c.sessionReady:
		if !c.sessionServing {
			return fmt.Errorf("failed to start session serving")
		}
		c.logger.LogInfo("XMPP client connected successfully", "jid", c.jidAddr.String())
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("timeout waiting for session to be ready")
	case <-c.ctx.Done():
		return fmt.Errorf("connection cancelled: %w", c.ctx.Err())
	}
}

// serveSession handles incoming XMPP stanzas through the multiplexer
func (c *Client) serveSession() {
	if c.session == nil || c.mux == nil {
		close(c.sessionReady) // Signal failure
		return
	}

	// Signal that the session is ready to serve
	c.sessionServing = true
	close(c.sessionReady)

	err := c.session.Serve(c.mux)
	if err != nil {
		c.sessionServing = false
		// Handle session serve errors
		// In production, you might want to log this error or attempt reconnection
		select {
		case <-c.ctx.Done():
			// Context cancelled, normal shutdown
			return
		default:
			// Unexpected error during session serve
			// Could trigger reconnection logic here
		}
	}
}

// Disconnect closes the XMPP connection
func (c *Client) Disconnect() error {
	if c.session == nil {
		return nil // Already disconnected
	}

	c.logger.LogInfo("Disconnecting XMPP client", "jid", c.jidAddr.String())

	// Send offline presence before disconnecting to properly leave rooms
	if err := c.SetOfflinePresence(); err != nil {
		c.logger.LogWarn("Failed to set offline presence before disconnect", "error", err)
		// Don't fail the disconnect for presence issues
	}

	// Close the session with a timeout to prevent hanging
	sessionCloseCtx, cancel := context.WithTimeout(context.Background(), defaultOperationTimeout)
	defer cancel()

	sessionCloseDone := make(chan error, 1)
	go func() {
		sessionCloseDone <- c.session.Close()
	}()

	select {
	case err := <-sessionCloseDone:
		c.session = nil
		if err != nil {
			c.logger.LogWarn("Error closing XMPP session", "error", err)
			return fmt.Errorf("failed to close XMPP session: %w", err)
		}
	case <-sessionCloseCtx.Done():
		c.logger.LogWarn("Timeout closing XMPP session, forcing disconnect")
		c.session = nil
		// Continue with cleanup even on timeout
	}

	// Cancel the client context
	if c.cancel != nil {
		c.cancel()
	}

	c.logger.LogInfo("XMPP client disconnected successfully")
	return nil
}

// JoinRoom joins an XMPP Multi-User Chat room
func (c *Client) JoinRoom(roomJID string) error {
	if c.session == nil {
		if err := c.Connect(); err != nil {
			return err
		}
	}

	if c.mucClient == nil {
		return fmt.Errorf("MUC client not initialized")
	}

	room, err := jid.Parse(roomJID)
	if err != nil {
		return fmt.Errorf("failed to parse room JID: %w", err)
	}

	// Use our username as nickname
	nickname := c.jidAddr.Localpart()
	roomWithNickname, err := room.WithResource(nickname)
	if err != nil {
		return fmt.Errorf("failed to add nickname to room JID: %w", err)
	}

	// Create a context with timeout for the join operation
	joinCtx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
	defer cancel()

	// Join the MUC room using the proper MUC client with timeout
	opts := []muc.Option{
		muc.MaxBytes(0), // Don't limit message history
	}

	// Run the join operation in a goroutine to avoid blocking
	errChan := make(chan error, 1)
	go func() {
		_, err := c.mucClient.Join(joinCtx, roomWithNickname, c.session, opts...)
		errChan <- err
	}()

	// Wait for join to complete or timeout
	select {
	case err := <-errChan:
		if err != nil {
			return fmt.Errorf("failed to join MUC room: %w", err)
		}
		return nil
	case <-joinCtx.Done():
		return fmt.Errorf("timeout joining MUC room %s", roomJID)
	}
}

// LeaveRoom leaves an XMPP Multi-User Chat room
func (c *Client) LeaveRoom(roomJID string) error {
	if c.session == nil {
		return fmt.Errorf("XMPP session not established")
	}

	if c.mucClient == nil {
		return fmt.Errorf("MUC client not initialized")
	}

	room, err := jid.Parse(roomJID)
	if err != nil {
		return fmt.Errorf("failed to parse room JID: %w", err)
	}

	// Use our username as nickname
	nickname := c.jidAddr.Localpart()
	roomWithNickname, err := room.WithResource(nickname)
	if err != nil {
		return fmt.Errorf("failed to add nickname to room JID: %w", err)
	}

	// Send unavailable presence to leave the room
	presence := stanza.Presence{
		From: c.jidAddr,
		To:   roomWithNickname,
		Type: stanza.UnavailablePresence,
	}

	if err := c.session.Encode(c.ctx, presence); err != nil {
		return fmt.Errorf("failed to send leave presence to MUC room: %w", err)
	}

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
		return nil, fmt.Errorf("failed to parse destination JID: %w", err)
	}

	// Create a context with timeout for the send operation
	sendCtx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
	defer cancel()

	// Create complete message with body
	fullMsg := XMPPMessage{
		Type: "groupchat",
		To:   to.String(),
		From: c.jidAddr.String(),
		Body: MessageBody{Text: req.Message},
	}

	// Send the message using the session encoder
	if err := c.session.Encode(sendCtx, fullMsg); err != nil {
		return nil, fmt.Errorf("failed to send message: %w", err)
	}

	// Generate a response
	response := &SendMessageResponse{
		StanzaID: fmt.Sprintf("msg_%d", time.Now().UnixNano()),
	}

	return response, nil
}

// SendDirectMessage sends a direct message to a specific user
func (c *Client) SendDirectMessage(userJID, message string) error {
	if c.session == nil {
		if err := c.Connect(); err != nil {
			return err
		}
	}

	to, err := jid.Parse(userJID)
	if err != nil {
		return fmt.Errorf("failed to parse user JID: %w", err)
	}

	// Create a context with timeout for the send operation
	sendCtx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
	defer cancel()

	// Create direct message using reusable structs
	msg := XMPPMessage{
		Type: "chat",
		To:   to.String(),
		From: c.jidAddr.String(),
		Body: MessageBody{Text: message},
	}

	// Send the message using the session encoder
	if err := c.session.Encode(sendCtx, msg); err != nil {
		return fmt.Errorf("failed to send direct message: %w", err)
	}

	return nil
}

// ResolveRoomAlias resolves a room alias to room JID
func (c *Client) ResolveRoomAlias(roomAlias string) (string, error) {
	// For XMPP, return the alias as-is if it's already a valid JID
	if _, err := jid.Parse(roomAlias); err == nil {
		return roomAlias, nil
	}
	return "", fmt.Errorf("invalid room alias/JID")
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
		return fmt.Errorf("XMPP session not established")
	}

	// Create presence stanza indicating we're available
	presence := stanza.Presence{
		Type: stanza.AvailablePresence,
		From: c.jidAddr,
	}

	// Send the presence stanza
	if err := c.session.Encode(c.ctx, presence); err != nil {
		return fmt.Errorf("failed to send online presence: %w", err)
	}

	return nil
}

// SetOfflinePresence sends an offline presence stanza to indicate the client is going offline
func (c *Client) SetOfflinePresence() error {
	if c.session == nil {
		return fmt.Errorf("XMPP session not established")
	}

	// Create presence stanza indicating we're unavailable
	presence := stanza.Presence{
		Type: stanza.UnavailablePresence,
		From: c.jidAddr,
	}

	// Create a context with timeout for the presence update
	ctx, cancel := context.WithTimeout(context.Background(), defaultOperationTimeout)
	defer cancel()

	// Send the presence stanza
	if err := c.session.Encode(ctx, presence); err != nil {
		return fmt.Errorf("failed to send offline presence: %w", err)
	}

	return nil
}

// CheckRoomExists verifies if an XMPP room exists and is accessible using disco#info
func (c *Client) CheckRoomExists(roomJID string) (bool, error) {
	if c.session == nil {
		return false, fmt.Errorf("XMPP session not established")
	}

	c.logger.LogDebug("Checking room existence using disco#info", "room_jid", roomJID)

	// Parse and validate the room JID
	roomAddr, err := jid.Parse(roomJID)
	if err != nil {
		c.logger.LogError("Invalid room JID", "room_jid", roomJID, "error", err)
		return false, fmt.Errorf("invalid room JID: %w", err)
	}

	// Set timeout for the disco query
	ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
	defer cancel()

	// Perform disco#info query to the room
	info, err := disco.GetInfo(ctx, "", roomAddr, c.session)
	if err != nil {
		// Check if it's a service-unavailable or item-not-found error
		if stanzaErr, ok := err.(stanza.Error); ok {
			c.logger.LogDebug("Received stanza error during disco#info query",
				"room_jid", roomJID,
				"error_condition", string(stanzaErr.Condition),
				"error_type", string(stanzaErr.Type))

			switch stanzaErr.Condition {
			case stanza.ServiceUnavailable, stanza.ItemNotFound:
				c.logger.LogDebug("Room does not exist", "room_jid", roomJID, "condition", string(stanzaErr.Condition))
				return false, nil // Room doesn't exist
			case stanza.Forbidden:
				c.logger.LogWarn("Access denied to room (room exists but not accessible)", "room_jid", roomJID)
				return false, fmt.Errorf("access denied to room %s", roomJID)
			case stanza.NotAuthorized:
				c.logger.LogWarn("Not authorized to query room (room exists but not queryable)", "room_jid", roomJID)
				return false, fmt.Errorf("not authorized to query room %s", roomJID)
			default:
				c.logger.LogError("Unexpected disco query error", "room_jid", roomJID, "condition", string(stanzaErr.Condition), "error", err)
				return false, fmt.Errorf("disco query failed: %w", err)
			}
		}
		c.logger.LogError("Disco query error", "room_jid", roomJID, "error", err)
		return false, fmt.Errorf("disco query error: %w", err)
	}

	c.logger.LogDebug("Received disco#info response, checking for MUC features",
		"room_jid", roomJID,
		"features_count", len(info.Features),
		"identities_count", len(info.Identity))

	// Verify it's actually a MUC room by checking features
	for _, feature := range info.Features {
		if feature.Var == muc.NS { // "http://jabber.org/protocol/muc"
			c.logger.LogDebug("Room exists and has MUC feature", "room_jid", roomJID)
			return true, nil
		}
	}

	// Check for conference identity as backup verification
	for _, identity := range info.Identity {
		if identity.Category == "conference" {
			c.logger.LogDebug("Room exists and has conference identity", "room_jid", roomJID, "identity_type", identity.Type)
			return true, nil
		}
	}

	// Log all features and identities for debugging
	c.logger.LogDebug("Room exists but doesn't appear to be a MUC room",
		"room_jid", roomJID,
		"features", func() []string {
			var features []string
			for _, f := range info.Features {
				features = append(features, f.Var)
			}
			return features
		}(),
		"identities", func() []string {
			var identities []string
			for _, i := range info.Identity {
				identities = append(identities, fmt.Sprintf("%s/%s", i.Category, i.Type))
			}
			return identities
		}())

	return false, nil
}

// Ping sends a lightweight ping to the XMPP server to test connectivity
func (c *Client) Ping() error {
	if c.session == nil {
		return fmt.Errorf("XMPP session not established")
	}

	c.logger.LogDebug("Sending XMPP ping to test connectivity")

	// Create a context with timeout for the ping
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()

	// Use disco#info query to server domain as a connectivity test
	// This is a standard, lightweight XMPP operation that all servers support
	_, err := disco.GetInfo(ctx, "", c.jidAddr.Domain(), c.session)
	if err != nil {
		duration := time.Since(start)
		c.logger.LogDebug("XMPP ping failed", "error", err, "duration", duration)
		return fmt.Errorf("XMPP server ping failed: %w", err)
	}

	duration := time.Since(start)
	c.logger.LogDebug("XMPP ping successful", "duration", duration)
	return nil
}
