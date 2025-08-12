package xmpp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/mattermost/mattermost/server/public/plugin"
	"mellium.im/xmpp/jid"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/config"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/logger"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/model"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/store/kvstore"
	xmppClient "github.com/mattermost/mattermost-plugin-bridge-xmpp/server/xmpp"
)

const (
	// KV store key prefixes for XMPP-specific data only
	ghostCredPrefix = "ghost_cred_" // Stores GhostUserData (JID, password, etc.)
)

// buildGhostUserKey generates a KV store key for ghost user data
func buildGhostUserKey(mattermostUserID string) string {
	return ghostCredPrefix + mattermostUserID
}

// GhostUserData represents persistent XMPP-specific ghost user information
type GhostUserData struct {
	MattermostUserID string `json:"mattermost_user_id"` // Mattermost user ID this ghost represents
	GhostJID         string `json:"ghost_jid"`          // XMPP JID of the ghost user
	GhostPassword    string `json:"ghost_password"`     // XMPP password for the ghost user
	Created          int64  `json:"created"`            // Timestamp when ghost was created
}

// UserManager manages XMPP users using XEP-0077 ghost users ONLY
// Only stores XMPP-specific data, uses Mattermost API for user info
type UserManager struct {
	bridgeType   string
	logger       logger.Logger
	kvstore      kvstore.KVStore
	api          plugin.API // Mattermost API for user info
	config       *config.Configuration
	configMu     sync.RWMutex
	bridgeClient *xmppClient.Client
	ctx          context.Context
	cancel       context.CancelFunc
}

// NewXMPPUserManager creates a new XMPP-specific user manager for ghost users only
func NewXMPPUserManager(bridgeType string, log logger.Logger, store kvstore.KVStore, api plugin.API, cfg *config.Configuration, bridgeClient *xmppClient.Client) model.BridgeUserManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &UserManager{
		bridgeType:   bridgeType,
		logger:       log,
		kvstore:      store,
		api:          api,
		config:       cfg,
		bridgeClient: bridgeClient,
		ctx:          ctx,
		cancel:       cancel,
	}
}

// generateGhostUserJID creates a JID for a new ghost user based on Mattermost user ID and configuration
func (m *UserManager) generateGhostUserJID(mattermostUserID string) string {
	m.configMu.RLock()
	prefix := m.config.XMPPGhostUserPrefix
	domain := m.config.GetXMPPGhostUserDomain()
	m.configMu.RUnlock()

	return fmt.Sprintf("%s%s@%s", prefix, mattermostUserID, domain)
}

// registerGhostUser registers a new ghost user via XEP-0077 In-Band Registration
func (m *UserManager) registerGhostUser(mattermostUserID, ghostJID, ghostPassword string) error {
	if m.bridgeClient == nil {
		return fmt.Errorf("bridge client not available for ghost user registration")
	}

	m.logger.LogDebug("Registering ghost user", "jid", ghostJID, "mattermost_user_id", mattermostUserID)

	// Get In-Band Registration handler from bridge client
	regHandler, err := m.bridgeClient.GetInBandRegistration()
	if err != nil {
		return fmt.Errorf("failed to get in-band registration handler: %w", err)
	}

	// Parse the ghost JID to get the server part
	ghostJIDParsed, err := jid.Parse(ghostJID)
	if err != nil {
		return fmt.Errorf("failed to parse ghost JID %s: %w", ghostJID, err)
	}

	// Register ghost user via XEP-0077
	request := &xmppClient.RegistrationRequest{
		Username: ghostJIDParsed.Localpart(),
		Password: ghostPassword,
	}

	response, err := regHandler.RegisterAccount(ghostJIDParsed.Domain(), request)
	if err != nil {
		return fmt.Errorf("XEP-0077 registration failed for %s: %w", ghostJID, err)
	}

	if !response.Success {
		return fmt.Errorf("XEP-0077 registration failed for %s: %s", ghostJID, response.Error)
	}

	m.logger.LogInfo("Ghost user registered successfully", "mattermost_user_id", mattermostUserID, "jid", ghostJID)
	return nil
}

// CreateUser creates a ghost user via XEP-0077 registration
func (m *UserManager) CreateUser(user model.BridgeUser) error {
	if err := user.Validate(); err != nil {
		return fmt.Errorf("invalid user: %w", err)
	}

	mattermostUserID := user.GetID()

	// Check if user already exists in KV store
	if m.HasUser(mattermostUserID) {
		return fmt.Errorf("user %s already exists", mattermostUserID)
	}

	m.logger.LogDebug("Creating XMPP ghost user", "bridge_type", m.bridgeType, "mattermost_user_id", mattermostUserID, "display_name", user.GetDisplayName())

	// Generate ghost user JID and password
	ghostJID := m.generateGhostUserJID(mattermostUserID)
	ghostPassword := generateSecurePassword()

	// Register ghost user via XEP-0077
	if err := m.registerGhostUser(mattermostUserID, ghostJID, ghostPassword); err != nil {
		return fmt.Errorf("failed to register ghost user %s: %w", mattermostUserID, err)
	}

	// Store ghost user data
	ghostData := &GhostUserData{
		MattermostUserID: mattermostUserID,
		GhostJID:         ghostJID,
		GhostPassword:    ghostPassword,
		Created:          m.getCurrentTimestamp(),
	}

	if err := m.storeGhostUserData(mattermostUserID, ghostData); err != nil {
		return fmt.Errorf("failed to store ghost user data for %s: %w", mattermostUserID, err)
	}

	m.logger.LogInfo("XMPP ghost user created successfully", "bridge_type", m.bridgeType, "mattermost_user_id", mattermostUserID)
	return nil
}

// GetUser retrieves a user by Mattermost user ID, creating XMPPUser from ghost data
func (m *UserManager) GetUser(mattermostUserID string) (model.BridgeUser, error) {
	// Check if ghost user data exists
	ghostData, err := m.loadGhostUserData(mattermostUserID)
	if err != nil {
		return nil, fmt.Errorf("ghost user not found for Mattermost user %s: %w", mattermostUserID, err)
	}

	// Create XMPPUser directly with ghost credentials
	m.configMu.RLock()
	cfg := m.config
	m.configMu.RUnlock()

	user := NewXMPPUser(mattermostUserID, mattermostUserID, ghostData.GhostJID, ghostData.GhostPassword, cfg, m.logger)

	// Ensure the user is connected
	if err := m.ensureUserConnected(user, mattermostUserID); err != nil {
		return nil, fmt.Errorf("failed to ensure ghost user is connected: %w", err)
	}

	return user, nil
}

// GetOrCreateUser retrieves a user by Mattermost user ID, creating a new ghost user if it doesn't exist
func (m *UserManager) GetOrCreateUser(mattermostUserID, displayName string) (model.BridgeUser, error) {
	// Try to get existing user first
	user, err := m.GetUser(mattermostUserID)
	if err == nil {
		return user, nil
	}

	m.logger.LogDebug("Ghost user not found, creating new ghost user", "mattermost_user_id", mattermostUserID, "display_name", displayName)

	// User doesn't exist, create a new ghost user
	m.configMu.RLock()
	cfg := m.config
	m.configMu.RUnlock()

	// Generate ghost user JID and password
	ghostJID := m.generateGhostUserJID(mattermostUserID)
	ghostPassword := generateSecurePassword()

	// Register ghost user via XEP-0077 first
	if err := m.registerGhostUser(mattermostUserID, ghostJID, ghostPassword); err != nil {
		return nil, fmt.Errorf("failed to register ghost user: %w", err)
	}

	// Create XMPPUser instance with the correct ghost credentials
	xmppUser := NewXMPPUser(mattermostUserID, displayName, ghostJID, ghostPassword, cfg, m.logger)

	// Store ghost user data
	ghostData := &GhostUserData{
		MattermostUserID: mattermostUserID,
		GhostJID:         ghostJID,
		GhostPassword:    ghostPassword,
		Created:          m.getCurrentTimestamp(),
	}

	if err := m.storeGhostUserData(mattermostUserID, ghostData); err != nil {
		m.logger.LogWarn("Failed to store ghost user data", "mattermost_user_id", mattermostUserID, "jid", ghostJID, "error", err)
		// Don't fail creation for storage issues, just log warning
	}

	// Ensure the newly created user is connected
	if err := m.ensureUserConnected(xmppUser, mattermostUserID); err != nil {
		return nil, fmt.Errorf("failed to connect newly created ghost user: %w", err)
	}

	m.logger.LogInfo("Ghost user created and connected successfully", "mattermost_user_id", mattermostUserID, "ghost_jid", ghostJID)
	return xmppUser, nil
}

// DeleteUser removes a user and cleans up ghost user account if cleanup is enabled
func (m *UserManager) DeleteUser(mattermostUserID string) error {
	m.logger.LogDebug("Deleting XMPP ghost user", "bridge_type", m.bridgeType, "mattermost_user_id", mattermostUserID)

	// Check if ghost user data exists
	if !m.HasUser(mattermostUserID) {
		return fmt.Errorf("ghost user not found for Mattermost user %s", mattermostUserID)
	}

	// Clean up ghost user account if cleanup is enabled
	m.configMu.RLock()
	shouldCleanup := m.config.IsGhostUserCleanupEnabled()
	m.configMu.RUnlock()

	if shouldCleanup {
		// Full cleanup: removes XMPP account + local data
		if err := m.cleanupGhostUser(mattermostUserID); err != nil {
			m.logger.LogWarn("Failed to cleanup ghost user account", "mattermost_user_id", mattermostUserID, "error", err)
			// Don't fail deletion for cleanup issues, continue with local removal
		}
	} else {
		// Only remove our local data, preserve XMPP account
		if err := m.removeGhostUserData(mattermostUserID); err != nil {
			m.logger.LogWarn("Failed to remove ghost user data from KV store", "mattermost_user_id", mattermostUserID, "error", err)
		}
	}

	m.logger.LogInfo("XMPP ghost user deleted successfully", "bridge_type", m.bridgeType, "mattermost_user_id", mattermostUserID)
	return nil
}

// ListUsers returns a list of all users from KV store
func (m *UserManager) ListUsers() []model.BridgeUser {
	keys, err := m.kvstore.ListKeysWithPrefix(0, 100, ghostCredPrefix)
	if err != nil {
		m.logger.LogWarn("Failed to list ghost user keys from KV store", "error", err)
		return []model.BridgeUser{}
	}

	users := make([]model.BridgeUser, 0, len(keys))
	for _, key := range keys {
		mattermostUserID := key[len(ghostCredPrefix):]
		user, err := m.GetUser(mattermostUserID)
		if err != nil {
			m.logger.LogWarn("Failed to load user", "mattermost_user_id", mattermostUserID, "error", err)
			continue
		}
		users = append(users, user)
	}

	return users
}

// HasUser checks if a ghost user exists for the given Mattermost user ID
func (m *UserManager) HasUser(mattermostUserID string) bool {
	key := buildGhostUserKey(mattermostUserID)
	value, err := m.kvstore.Get(key)
	return err == nil && !bytes.Equal(value, []byte{}) // Check if key exists and is not empty
}

// Start initializes the user manager and starts all ghost users from KV store
func (m *UserManager) Start(ctx context.Context) error {
	m.logger.LogDebug("Starting XMPP ghost user manager", "bridge_type", m.bridgeType)

	m.ctx = ctx

	// Get all users from KV store and start them
	users := m.ListUsers()
	startedCount := 0

	for _, user := range users {
		if err := user.Start(ctx); err != nil {
			m.logger.LogWarn("Failed to start ghost user during manager startup", "bridge_type", m.bridgeType, "user_id", user.GetID(), "error", err)
			// Continue starting other users even if one fails
		} else {
			startedCount++
		}
	}

	m.logger.LogInfo("XMPP ghost user manager started", "bridge_type", m.bridgeType, "user_count", startedCount)
	return nil
}

// Stop shuts down the user manager and all ghost users
func (m *UserManager) Stop() error {
	m.logger.LogDebug("Stopping XMPP ghost user manager", "bridge_type", m.bridgeType)

	if m.cancel != nil {
		m.cancel()
	}

	// Get all users from KV store and stop them
	users := m.ListUsers()
	for _, user := range users {
		if err := user.Stop(); err != nil {
			m.logger.LogWarn("Error stopping ghost user during manager shutdown", "bridge_type", m.bridgeType, "user_id", user.GetID(), "error", err)
		}
	}

	m.logger.LogInfo("XMPP ghost user manager stopped", "bridge_type", m.bridgeType)
	return nil
}

// UpdateConfiguration updates configuration
func (m *UserManager) UpdateConfiguration(cfg *config.Configuration) error {
	m.logger.LogDebug("Updating configuration for XMPP ghost user manager", "bridge_type", m.bridgeType)

	m.configMu.Lock()
	m.config = cfg
	m.configMu.Unlock()

	m.logger.LogInfo("XMPP ghost user manager configuration updated", "bridge_type", m.bridgeType)
	return nil
}

// GetBridgeType returns the bridge type this manager handles
func (m *UserManager) GetBridgeType() string {
	return m.bridgeType
}

// KV store operations for ghost user data only

func (m *UserManager) storeGhostUserData(mattermostUserID string, data *GhostUserData) error {
	key := buildGhostUserKey(mattermostUserID)
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal ghost user data: %w", err)
	}
	return m.kvstore.Set(key, jsonData)
}

func (m *UserManager) loadGhostUserData(mattermostUserID string) (*GhostUserData, error) {
	key := buildGhostUserKey(mattermostUserID)
	data, err := m.kvstore.Get(key)
	if err != nil {
		return nil, fmt.Errorf("ghost user data not found: %w", err)
	}

	var ghostData GhostUserData
	if err := json.Unmarshal(data, &ghostData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal ghost user data: %w", err)
	}

	return &ghostData, nil
}

func (m *UserManager) removeGhostUserData(mattermostUserID string) error {
	key := buildGhostUserKey(mattermostUserID)
	return m.kvstore.Delete(key)
}

func (m *UserManager) cleanupGhostUser(mattermostUserID string) error {
	ghostData, err := m.loadGhostUserData(mattermostUserID)
	if err != nil {
		// No ghost data found, nothing to cleanup
		return nil
	}

	// Get In-Band Registration handler from bridge client
	regHandler, err := m.bridgeClient.GetInBandRegistration()
	if err != nil {
		return fmt.Errorf("failed to get in-band registration handler for cleanup: %w", err)
	}

	// Parse the ghost JID to get the server part
	ghostJIDParsed, err := jid.Parse(ghostData.GhostJID)
	if err != nil {
		return fmt.Errorf("failed to parse ghost JID %s for cleanup: %w", ghostData.GhostJID, err)
	}

	// Unregister the ghost user account via XEP-0077
	cancellationRequest := &xmppClient.CancellationRequest{
		Username: ghostJIDParsed.Localpart(), // Extract username from ghost JID
	}
	response, err := regHandler.CancelRegistration(ghostJIDParsed.Domain(), cancellationRequest)
	if err != nil {
		return fmt.Errorf("failed to cancel registration for ghost user %s: %w", ghostData.GhostJID, err)
	}

	if !response.Success {
		m.logger.LogWarn("XEP-0077 registration cancellation failed", "jid", ghostData.GhostJID, "error", response.Error)
		// Continue with cleanup even if cancellation failed
	}

	// Remove ghost user data from KV store
	if err := m.removeGhostUserData(mattermostUserID); err != nil {
		m.logger.LogWarn("Failed to delete ghost user data from KV store", "mattermost_user_id", mattermostUserID, "error", err)
	}

	m.logger.LogInfo("Ghost user account cleaned up successfully", "mattermost_user_id", mattermostUserID, "jid", ghostData.GhostJID)
	return nil
}

// ensureUserConnected ensures that a ghost user is connected to XMPP
func (m *UserManager) ensureUserConnected(xmppUser *User, mattermostUserID string) error {
	// Check if user is already connected
	if xmppUser.IsConnected() {
		m.logger.LogDebug("Ghost user already connected", "mattermost_user_id", mattermostUserID, "ghost_jid", xmppUser.GetJID())
		return nil
	}

	m.logger.LogDebug("Starting ghost user connection", "mattermost_user_id", mattermostUserID, "ghost_jid", xmppUser.GetJID())

	// Start the user (this will connect it to XMPP)
	if err := xmppUser.Start(m.ctx); err != nil {
		return fmt.Errorf("failed to start ghost user %s: %w", xmppUser.GetJID(), err)
	}

	// Verify connection was successful
	if !xmppUser.IsConnected() {
		return fmt.Errorf("ghost user %s failed to connect after start", xmppUser.GetJID())
	}

	m.logger.LogInfo("Ghost user connected successfully", "mattermost_user_id", mattermostUserID, "ghost_jid", xmppUser.GetJID())
	return nil
}

// Helper functions

func generateSecurePassword() string {
	// TODO: Implement secure password generation using crypto/rand
	return "temp_secure_password_123"
}

func (m *UserManager) getCurrentTimestamp() int64 {
	// TODO: Use proper time source (time.Now().Unix())
	return 0
}
