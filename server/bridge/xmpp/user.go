package xmpp

import (
	"context"
	"crypto/tls"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/config"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/logger"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/model"
	xmppClient "github.com/mattermost/mattermost-plugin-bridge-xmpp/server/xmpp"
)

// XMPPUser represents an XMPP user that implements the BridgeUser interface
type XMPPUser struct {
	// User identity
	id          string
	displayName string
	jid         string

	// XMPP client
	client *xmppClient.Client

	// State management
	state     model.UserState
	stateMu   sync.RWMutex
	connected atomic.Bool

	// Configuration
	config *config.Configuration

	// Goroutine lifecycle
	ctx    context.Context
	cancel context.CancelFunc

	// Logger
	logger logger.Logger
}

// NewXMPPUser creates a new XMPP user
func NewXMPPUser(id, displayName, jid string, cfg *config.Configuration, logger logger.Logger) *XMPPUser {
	ctx, cancel := context.WithCancel(context.Background())
	
	// Create TLS config based on certificate verification setting
	tlsConfig := &tls.Config{
		InsecureSkipVerify: cfg.XMPPInsecureSkipVerify,
	}

	// Create XMPP client for this user
	client := xmppClient.NewClientWithTLS(
		cfg.XMPPServerURL,
		jid,
		cfg.XMPPPassword, // This might need to be user-specific in the future
		cfg.GetXMPPResource(),
		id, // Use user ID as remote ID
		tlsConfig,
		logger,
	)

	return &XMPPUser{
		id:          id,
		displayName: displayName,
		jid:         jid,
		client:      client,
		state:       model.UserStateOffline,
		config:      cfg,
		ctx:         ctx,
		cancel:      cancel,
		logger:      logger,
	}
}

// Validation
func (u *XMPPUser) Validate() error {
	if u.id == "" {
		return fmt.Errorf("user ID cannot be empty")
	}
	if u.jid == "" {
		return fmt.Errorf("JID cannot be empty")
	}
	if u.config == nil {
		return fmt.Errorf("configuration cannot be nil")
	}
	if u.config.XMPPServerURL == "" {
		return fmt.Errorf("XMPP server URL cannot be empty")
	}
	if u.client == nil {
		return fmt.Errorf("XMPP client cannot be nil")
	}
	return nil
}

// Identity (bridge-agnostic)
func (u *XMPPUser) GetID() string {
	return u.id
}

func (u *XMPPUser) GetDisplayName() string {
	return u.displayName
}

// State management
func (u *XMPPUser) GetState() model.UserState {
	u.stateMu.RLock()
	defer u.stateMu.RUnlock()
	return u.state
}

func (u *XMPPUser) SetState(state model.UserState) error {
	u.stateMu.Lock()
	defer u.stateMu.Unlock()
	
	u.logger.LogDebug("Changing XMPP user state", "user_id", u.id, "old_state", u.state, "new_state", state)
	u.state = state
	
	// TODO: Send presence update to XMPP server based on state
	// This would involve mapping UserState to XMPP presence types
	
	return nil
}

// Channel operations
func (u *XMPPUser) JoinChannel(channelID string) error {
	if !u.connected.Load() {
		return fmt.Errorf("user %s is not connected", u.id)
	}
	
	u.logger.LogDebug("XMPP user joining channel", "user_id", u.id, "channel_id", channelID)
	
	// For XMPP, channelID is the room JID
	err := u.client.JoinRoom(channelID)
	if err != nil {
		return fmt.Errorf("failed to join XMPP room %s: %w", channelID, err)
	}
	
	u.logger.LogInfo("XMPP user joined channel", "user_id", u.id, "channel_id", channelID)
	return nil
}

func (u *XMPPUser) LeaveChannel(channelID string) error {
	if !u.connected.Load() {
		return fmt.Errorf("user %s is not connected", u.id)
	}
	
	u.logger.LogDebug("XMPP user leaving channel", "user_id", u.id, "channel_id", channelID)
	
	// For XMPP, channelID is the room JID
	err := u.client.LeaveRoom(channelID)
	if err != nil {
		return fmt.Errorf("failed to leave XMPP room %s: %w", channelID, err)
	}
	
	u.logger.LogInfo("XMPP user left channel", "user_id", u.id, "channel_id", channelID)
	return nil
}

func (u *XMPPUser) SendMessageToChannel(channelID, message string) error {
	if !u.connected.Load() {
		return fmt.Errorf("user %s is not connected", u.id)
	}
	
	u.logger.LogDebug("XMPP user sending message to channel", "user_id", u.id, "channel_id", channelID)
	
	// Create message request for XMPP
	req := xmppClient.MessageRequest{
		RoomJID:      channelID,
		GhostUserJID: u.jid,
		Message:      message,
	}
	
	_, err := u.client.SendMessage(req)
	if err != nil {
		return fmt.Errorf("failed to send message to XMPP room %s: %w", channelID, err)
	}
	
	u.logger.LogDebug("XMPP user sent message to channel", "user_id", u.id, "channel_id", channelID)
	return nil
}

// Connection lifecycle
func (u *XMPPUser) Connect() error {
	u.logger.LogDebug("Connecting XMPP user", "user_id", u.id, "jid", u.jid)
	
	err := u.client.Connect()
	if err != nil {
		u.connected.Store(false)
		return fmt.Errorf("failed to connect XMPP user %s: %w", u.id, err)
	}
	
	u.connected.Store(true)
	u.logger.LogInfo("XMPP user connected", "user_id", u.id, "jid", u.jid)
	
	// Set online presence after successful connection
	if err := u.client.SetOnlinePresence(); err != nil {
		u.logger.LogWarn("Failed to set online presence for XMPP user", "user_id", u.id, "error", err)
		// Don't fail the connection for presence issues
	}
	
	// Update state to online
	_ = u.SetState(model.UserStateOnline)
	
	return nil
}

func (u *XMPPUser) Disconnect() error {
	u.logger.LogDebug("Disconnecting XMPP user", "user_id", u.id, "jid", u.jid)
	
	if u.client == nil {
		return nil
	}
	
	err := u.client.Disconnect()
	if err != nil {
		u.logger.LogWarn("Error disconnecting XMPP user", "user_id", u.id, "error", err)
	}
	
	u.connected.Store(false)
	_ = u.SetState(model.UserStateOffline)
	
	u.logger.LogInfo("XMPP user disconnected", "user_id", u.id, "jid", u.jid)
	return err
}

func (u *XMPPUser) IsConnected() bool {
	return u.connected.Load()
}

func (u *XMPPUser) Ping() error {
	if !u.connected.Load() {
		return fmt.Errorf("XMPP user %s is not connected", u.id)
	}
	
	if u.client == nil {
		return fmt.Errorf("XMPP client not initialized for user %s", u.id)
	}
	
	return u.client.Ping()
}

// CheckChannelExists checks if an XMPP room/channel exists
func (u *XMPPUser) CheckChannelExists(channelID string) (bool, error) {
	if !u.connected.Load() {
		return false, fmt.Errorf("XMPP user %s is not connected", u.id)
	}
	
	if u.client == nil {
		return false, fmt.Errorf("XMPP client not initialized for user %s", u.id)
	}
	
	return u.client.CheckRoomExists(channelID)
}

// Goroutine lifecycle
func (u *XMPPUser) Start(ctx context.Context) error {
	u.logger.LogDebug("Starting XMPP user", "user_id", u.id, "jid", u.jid)
	
	// Update context
	u.ctx = ctx
	
	// Connect to XMPP server
	if err := u.Connect(); err != nil {
		return fmt.Errorf("failed to start XMPP user %s: %w", u.id, err)
	}
	
	// Start connection monitoring in a goroutine
	go u.connectionMonitor()
	
	u.logger.LogInfo("XMPP user started", "user_id", u.id, "jid", u.jid)
	return nil
}

func (u *XMPPUser) Stop() error {
	u.logger.LogDebug("Stopping XMPP user", "user_id", u.id, "jid", u.jid)
	
	// Cancel context to stop goroutines
	if u.cancel != nil {
		u.cancel()
	}
	
	// Disconnect from XMPP server
	if err := u.Disconnect(); err != nil {
		u.logger.LogWarn("Error disconnecting XMPP user during stop", "user_id", u.id, "error", err)
	}
	
	u.logger.LogInfo("XMPP user stopped", "user_id", u.id, "jid", u.jid)
	return nil
}

// connectionMonitor monitors the XMPP connection for this user
func (u *XMPPUser) connectionMonitor() {
	u.logger.LogDebug("Starting connection monitor for XMPP user", "user_id", u.id)
	
	// Simple monitoring - check connection periodically
	for {
		select {
		case <-u.ctx.Done():
			u.logger.LogDebug("Connection monitor stopped for XMPP user", "user_id", u.id)
			return
		default:
			// Check connection every 30 seconds
			if u.connected.Load() {
				if err := u.client.Ping(); err != nil {
					u.logger.LogWarn("Connection check failed for XMPP user", "user_id", u.id, "error", err)
					u.connected.Store(false)
					_ = u.SetState(model.UserStateOffline)
					
					// TODO: Implement reconnection logic if needed
				}
			}
			
			// Wait before next check
			timeoutCtx, cancel := context.WithTimeout(u.ctx, 30 * time.Second) // 30 seconds
			select {
			case <-u.ctx.Done():
				cancel()
				return
			case <-timeoutCtx.Done():
				cancel()
				continue
			}
		}
	}
}

// GetJID returns the XMPP JID for this user (XMPP-specific method)
func (u *XMPPUser) GetJID() string {
	return u.jid
}

// GetClient returns the underlying XMPP client (for advanced operations)
func (u *XMPPUser) GetClient() *xmppClient.Client {
	return u.client
}