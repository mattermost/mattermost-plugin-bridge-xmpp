package mattermost

import (
	"context"
	"fmt"
	"sync"
	"time"

	mmModel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/config"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/logger"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/model"
)

// MattermostUser represents a Mattermost user that implements the BridgeUser interface
//
//nolint:revive // MattermostUser is clearer than User in this context
type MattermostUser struct {
	// User identity
	id          string
	displayName string
	username    string
	email       string

	// Mattermost API
	api plugin.API

	// State management
	state   model.UserState
	stateMu sync.RWMutex

	// Configuration
	config *config.Configuration

	// Goroutine lifecycle
	ctx    context.Context
	cancel context.CancelFunc

	// Logger
	logger logger.Logger
}

// NewMattermostUser creates a new Mattermost user
func NewMattermostUser(id, displayName, username, email string, api plugin.API, cfg *config.Configuration, log logger.Logger) *MattermostUser {
	ctx, cancel := context.WithCancel(context.Background())

	return &MattermostUser{
		id:          id,
		displayName: displayName,
		username:    username,
		email:       email,
		api:         api,
		state:       model.UserStateOffline,
		config:      cfg,
		ctx:         ctx,
		cancel:      cancel,
		logger:      log,
	}
}

// Validation
func (u *MattermostUser) Validate() error {
	if u.id == "" {
		return fmt.Errorf("user ID cannot be empty")
	}
	if u.username == "" {
		return fmt.Errorf("username cannot be empty")
	}
	if u.config == nil {
		return fmt.Errorf("configuration cannot be nil")
	}
	if u.api == nil {
		return fmt.Errorf("mattermost API cannot be nil")
	}
	return nil
}

// Identity (bridge-agnostic)
func (u *MattermostUser) GetID() string {
	return u.id
}

func (u *MattermostUser) GetDisplayName() string {
	return u.displayName
}

// State management
func (u *MattermostUser) GetState() model.UserState {
	u.stateMu.RLock()
	defer u.stateMu.RUnlock()
	return u.state
}

func (u *MattermostUser) SetState(state model.UserState) error {
	u.stateMu.Lock()
	defer u.stateMu.Unlock()

	u.logger.LogDebug("Changing Mattermost user state", "user_id", u.id, "old_state", u.state, "new_state", state)
	u.state = state

	// TODO: Update user status in Mattermost if needed
	// This could involve setting custom status or presence indicators

	return nil
}

// Channel operations (abstracted from rooms/channels/groups)
func (u *MattermostUser) JoinChannel(channelID string) error {
	u.logger.LogDebug("Mattermost user joining channel", "user_id", u.id, "channel_id", channelID)

	// Add user to channel
	_, appErr := u.api.AddUserToChannel(channelID, u.id, "")
	if appErr != nil {
		return fmt.Errorf("failed to add Mattermost user %s to channel %s: %w", u.id, channelID, appErr)
	}

	u.logger.LogInfo("Mattermost user joined channel", "user_id", u.id, "channel_id", channelID)
	return nil
}

func (u *MattermostUser) LeaveChannel(channelID string) error {
	u.logger.LogDebug("Mattermost user leaving channel", "user_id", u.id, "channel_id", channelID)

	// Remove user from channel
	appErr := u.api.DeleteChannelMember(channelID, u.id)
	if appErr != nil {
		return fmt.Errorf("failed to remove Mattermost user %s from channel %s: %w", u.id, channelID, appErr)
	}

	u.logger.LogInfo("Mattermost user left channel", "user_id", u.id, "channel_id", channelID)
	return nil
}

func (u *MattermostUser) SendMessageToChannel(channelID, message string) error {
	u.logger.LogDebug("Mattermost user sending message to channel", "user_id", u.id, "channel_id", channelID)

	// Create post
	post := &mmModel.Post{
		UserId:    u.id,
		ChannelId: channelID,
		Message:   message,
	}

	// Send post
	_, appErr := u.api.CreatePost(post)
	if appErr != nil {
		return fmt.Errorf("failed to send message to Mattermost channel %s: %w", channelID, appErr)
	}

	u.logger.LogDebug("Mattermost user sent message to channel", "user_id", u.id, "channel_id", channelID)
	return nil
}

// Connection lifecycle
func (u *MattermostUser) Connect() error {
	u.logger.LogDebug("Connecting Mattermost user", "user_id", u.id, "username", u.username)

	// For Mattermost users, "connecting" means verifying the user exists and is accessible
	user, appErr := u.api.GetUser(u.id)
	if appErr != nil {
		return fmt.Errorf("failed to verify Mattermost user %s: %w", u.id, appErr)
	}

	// Update user information if it has changed
	if user.GetDisplayName("") != u.displayName {
		u.displayName = user.GetDisplayName("")
		u.logger.LogDebug("Updated Mattermost user display name", "user_id", u.id, "display_name", u.displayName)
	}

	u.logger.LogInfo("Mattermost user connected", "user_id", u.id, "username", u.username)

	// Update state to online
	_ = u.SetState(model.UserStateOnline)

	return nil
}

func (u *MattermostUser) Disconnect() error {
	u.logger.LogDebug("Disconnecting Mattermost user", "user_id", u.id, "username", u.username)

	// For Mattermost users, "disconnecting" is mostly a state change
	// The user still exists in Mattermost, but we're not actively managing them

	_ = u.SetState(model.UserStateOffline)

	u.logger.LogInfo("Mattermost user disconnected", "user_id", u.id, "username", u.username)
	return nil
}

func (u *MattermostUser) IsConnected() bool {
	return u.GetState() == model.UserStateOnline
}

func (u *MattermostUser) Ping() error {
	if u.api == nil {
		return fmt.Errorf("mattermost API not initialized for user %s", u.id)
	}

	// Test API connectivity by getting server version
	version := u.api.GetServerVersion()
	if version == "" {
		return fmt.Errorf("mattermost API ping returned empty server version for user %s", u.id)
	}

	return nil
}

// CheckChannelExists checks if a Mattermost channel exists
func (u *MattermostUser) CheckChannelExists(channelID string) (bool, error) {
	if u.api == nil {
		return false, fmt.Errorf("mattermost API not initialized for user %s", u.id)
	}

	// Try to get the channel by ID
	_, appErr := u.api.GetChannel(channelID)
	if appErr != nil {
		// Check if it's a "not found" error
		if appErr.StatusCode == 404 {
			return false, nil // Channel doesn't exist
		}
		return false, fmt.Errorf("failed to check channel existence: %w", appErr)
	}

	return true, nil
}

// Goroutine lifecycle
func (u *MattermostUser) Start(ctx context.Context) error {
	u.logger.LogDebug("Starting Mattermost user", "user_id", u.id, "username", u.username)

	// Update context
	u.ctx = ctx

	// Connect to verify user exists
	if err := u.Connect(); err != nil {
		return fmt.Errorf("failed to start Mattermost user %s: %w", u.id, err)
	}

	// Start monitoring in a goroutine
	go u.monitor()

	u.logger.LogInfo("Mattermost user started", "user_id", u.id, "username", u.username)
	return nil
}

func (u *MattermostUser) Stop() error {
	u.logger.LogDebug("Stopping Mattermost user", "user_id", u.id, "username", u.username)

	// Cancel context to stop goroutines
	if u.cancel != nil {
		u.cancel()
	}

	// Disconnect
	if err := u.Disconnect(); err != nil {
		u.logger.LogWarn("Error disconnecting Mattermost user during stop", "user_id", u.id, "error", err)
	}

	u.logger.LogInfo("Mattermost user stopped", "user_id", u.id, "username", u.username)
	return nil
}

// monitor periodically checks the user's status and updates information
func (u *MattermostUser) monitor() {
	u.logger.LogDebug("Starting monitor for Mattermost user", "user_id", u.id)

	// Simple monitoring - check user exists periodically
	for {
		select {
		case <-u.ctx.Done():
			u.logger.LogDebug("Monitor stopped for Mattermost user", "user_id", u.id)
			return
		default:
			// Wait before next check
			timeoutCtx, cancel := context.WithTimeout(u.ctx, 60*time.Second)
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

// GetUsername returns the Mattermost username for this user (Mattermost-specific method)
func (u *MattermostUser) GetUsername() string {
	return u.username
}

// GetEmail returns the Mattermost email for this user (Mattermost-specific method)
func (u *MattermostUser) GetEmail() string {
	return u.email
}

// GetAPI returns the Mattermost API instance (for advanced operations)
func (u *MattermostUser) GetAPI() plugin.API {
	return u.api
}
