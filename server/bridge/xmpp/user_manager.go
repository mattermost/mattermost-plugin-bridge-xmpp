package xmpp

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"sync"
	"time"

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
	LastActivity     int64  `json:"last_activity"`      // Unix timestamp of last activity
	LifecycleEnabled bool   `json:"lifecycle_enabled"`  // Whether this user should be subject to inactivity checking
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

	// Node identification for HA environments
	nodeID string // Unique identifier for this Mattermost node (from api.GetDiagnosticId)

	// Connection caching to prevent connection leaks
	activeUsers   map[string]*User // Cache of connected users on THIS node
	activeUsersMu sync.RWMutex     // Protects activeUsers map
}

// NewXMPPUserManager creates a new XMPP-specific user manager for ghost users only
func NewXMPPUserManager(bridgeType string, log logger.Logger, store kvstore.KVStore, api plugin.API, cfg *config.Configuration, bridgeClient *xmppClient.Client) model.BridgeUserManager {
	ctx, cancel := context.WithCancel(context.Background())

	// Get unique node ID from Mattermost API for HA environments
	nodeID := api.GetDiagnosticId()
	log.LogDebug("Initializing XMPP user manager", "bridge_type", bridgeType, "node_id", nodeID[:8])

	return &UserManager{
		bridgeType:   bridgeType,
		logger:       log,
		kvstore:      store,
		api:          api,
		config:       cfg,
		bridgeClient: bridgeClient,
		ctx:          ctx,
		cancel:       cancel,
		nodeID:       nodeID,
		activeUsers:  make(map[string]*User), // Initialize connection cache
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

// GetUser retrieves a user by Mattermost user ID, checking cache first, then creating XMPPUser from ghost data
func (m *UserManager) GetUser(mattermostUserID string) (model.BridgeUser, error) {
	// First check the connection cache
	if cachedUser, found := m.getCachedUser(mattermostUserID); found {
		m.logger.LogDebug("Found user in connection cache", "user_id", mattermostUserID, "ghost_jid", cachedUser.GetJID())
		return cachedUser, nil
	}

	// Check if ghost user data exists
	ghostData, err := m.loadGhostUserData(mattermostUserID)
	if err != nil {
		return nil, fmt.Errorf("ghost user not found for Mattermost user %s: %w", mattermostUserID, err)
	}

	// Create XMPPUser directly with ghost credentials and activity data
	m.configMu.RLock()
	cfg := m.config
	m.configMu.RUnlock()

	// Handle migration of existing users without activity data
	lastActivity := time.Now()
	if ghostData.LastActivity > 0 {
		lastActivity = time.Unix(ghostData.LastActivity, 0)
	} else {
		// Update the KV store with the migration data
		ghostData.LastActivity = lastActivity.Unix()
		ghostData.LifecycleEnabled = true
		_ = m.storeGhostUserData(mattermostUserID, ghostData) // Don't fail if storage update fails
	}

	user := m.createXMPPUserWithActivity(mattermostUserID, mattermostUserID, ghostData.GhostJID, ghostData.GhostPassword, cfg, m.logger, lastActivity, ghostData.LifecycleEnabled)

	// Ensure the user is connected
	if err := m.ensureUserConnected(user, mattermostUserID); err != nil {
		return nil, fmt.Errorf("failed to ensure ghost user is connected: %w", err)
	}

	// Cache the connected user to prevent connection leaks
	m.cacheUser(mattermostUserID, user)

	return user, nil
}

// GetOrCreateUser retrieves a user by Mattermost user ID, creating a new ghost user if it doesn't exist
func (m *UserManager) GetOrCreateUser(mattermostUserID, displayName string) (model.BridgeUser, error) {
	// First check the connection cache
	if cachedUser, found := m.getCachedUser(mattermostUserID); found {
		m.logger.LogDebug("Found user in connection cache", "user_id", mattermostUserID, "ghost_jid", cachedUser.GetJID())
		return cachedUser, nil
	}

	// Try to get existing user from KV store
	user, err := m.GetUser(mattermostUserID)
	if err == nil {
		// GetUser already cached the user, so just return it
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

	// Initialize activity data for new user
	now := time.Now()

	// Create XMPPUser instance with the correct ghost credentials and activity data
	xmppUser := m.createXMPPUserWithActivity(mattermostUserID, displayName, ghostJID, ghostPassword, cfg, m.logger, now, true)

	// Store ghost user data with activity tracking
	ghostData := &GhostUserData{
		MattermostUserID: mattermostUserID,
		GhostJID:         ghostJID,
		GhostPassword:    ghostPassword,
		Created:          now.Unix(),
		LastActivity:     now.Unix(),
		LifecycleEnabled: true,
	}

	if err := m.storeGhostUserData(mattermostUserID, ghostData); err != nil {
		m.logger.LogWarn("Failed to store ghost user data", "mattermost_user_id", mattermostUserID, "jid", ghostJID, "error", err)
		// Don't fail creation for storage issues, just log warning
	}

	// Ensure the newly created user is connected
	if err := m.ensureUserConnected(xmppUser, mattermostUserID); err != nil {
		return nil, fmt.Errorf("failed to connect newly created ghost user: %w", err)
	}

	// Cache the connected user to prevent connection leaks
	m.cacheUser(mattermostUserID, xmppUser)

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

	// Disconnect and remove from cache if user is currently active
	if cachedUser, found := m.getCachedUser(mattermostUserID); found {
		if err := cachedUser.Disconnect(); err != nil {
			m.logger.LogWarn("Failed to disconnect cached user during deletion", "mattermost_user_id", mattermostUserID, "error", err)
		}
		m.removeCachedUser(mattermostUserID)
		m.logger.LogDebug("Disconnected and removed user from cache during deletion", "mattermost_user_id", mattermostUserID)
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
			// Enable lifecycle management for ghost users
			if xmppUser, ok := user.(*User); ok {
				xmppUser.SetLifecycleManagement(true)
			}
		}
	}

	// Start the lifecycle management goroutine
	go m.lifecycleManager()

	m.logger.LogInfo("XMPP ghost user manager started", "bridge_type", m.bridgeType, "user_count", startedCount)
	return nil
}

// Stop shuts down the user manager and all ghost users
func (m *UserManager) Stop() error {
	m.logger.LogDebug("Stopping XMPP ghost user manager", "bridge_type", m.bridgeType)

	// Cancel context to stop background goroutines
	if m.cancel != nil {
		m.cancel()
	}

	// Gracefully shutdown all cached connections first (much faster than ListUsers)
	cachedUsers := m.getCachedUsers()
	disconnectedCount := 0

	for _, user := range cachedUsers {
		if err := user.Stop(); err != nil {
			m.logger.LogWarn("Error stopping cached ghost user during manager shutdown", "bridge_type", m.bridgeType, "user_id", user.GetID(), "error", err)
		} else {
			disconnectedCount++
		}
	}

	// Clear the entire cache
	m.activeUsersMu.Lock()
	m.activeUsers = make(map[string]*User)
	m.activeUsersMu.Unlock()

	// Also check for any users not in cache and stop them (fallback)
	users := m.ListUsers()
	fallbackStoppedCount := 0
	for _, user := range users {
		if err := user.Stop(); err != nil {
			m.logger.LogWarn("Error stopping ghost user during manager shutdown", "bridge_type", m.bridgeType, "user_id", user.GetID(), "error", err)
		} else {
			fallbackStoppedCount++
		}
	}

	m.logger.LogInfo("XMPP ghost user manager stopped",
		"bridge_type", m.bridgeType,
		"cached_users_stopped", disconnectedCount,
		"fallback_users_stopped", fallbackStoppedCount)
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

// UpdateUserActivity updates both the in-memory and persisted activity timestamp for a user
func (m *UserManager) UpdateUserActivity(mattermostUserID string) error {
	// Load existing ghost user data
	ghostData, err := m.loadGhostUserData(mattermostUserID)
	if err != nil {
		return fmt.Errorf("failed to load ghost user data for activity update: %w", err)
	}

	// Update the activity timestamp
	now := time.Now()
	ghostData.LastActivity = now.Unix()

	// Store the updated data
	if err := m.storeGhostUserData(mattermostUserID, ghostData); err != nil {
		return fmt.Errorf("failed to persist activity update: %w", err)
	}

	m.logger.LogDebug("Updated user activity in KV store",
		"user_id", mattermostUserID,
		"timestamp", now)

	return nil
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

// lifecycleManager runs periodically to check for inactive ghost users and disconnect them
func (m *UserManager) lifecycleManager() {
	m.logger.LogDebug("Starting lifecycle manager for ghost user cleanup")

	ticker := time.NewTicker(ghostUserActivityCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			m.logger.LogDebug("Lifecycle manager stopped due to context cancellation")
			return
		case <-ticker.C:
			m.checkAndDisconnectInactiveUsers()
		}
	}
}

// checkAndDisconnectInactiveUsers checks all cached users for inactivity and disconnects inactive ghost users
func (m *UserManager) checkAndDisconnectInactiveUsers() {
	m.logger.LogDebug("Checking cached users for inactivity")

	// Get all currently cached users (this is much more efficient than ListUsers)
	cachedUsers := m.getCachedUsers()
	inactiveCount := 0
	disconnectedCount := 0

	for _, xmppUser := range cachedUsers {
		// Only check users that have lifecycle management enabled
		if !xmppUser.IsLifecycleManaged() {
			continue
		}

		// Check if user is connected and inactive
		if xmppUser.IsConnected() && xmppUser.IsInactive(ghostUserInactivityTimeout) {
			inactiveCount++
			lastActivity := xmppUser.GetLastActivity()
			inactiveDuration := time.Since(lastActivity)

			m.logger.LogInfo("Disconnecting inactive ghost user",
				"user_id", xmppUser.GetID(),
				"jid", xmppUser.GetJID(),
				"last_activity", lastActivity,
				"inactive_duration", inactiveDuration)

			// Gracefully disconnect the inactive user
			if err := xmppUser.Disconnect(); err != nil {
				m.logger.LogWarn("Failed to disconnect inactive ghost user",
					"user_id", xmppUser.GetID(),
					"jid", xmppUser.GetJID(),
					"error", err)
			} else {
				disconnectedCount++
				// Remove disconnected user from cache to free memory
				m.removeCachedUser(xmppUser.GetID())
				m.logger.LogDebug("Successfully disconnected and removed inactive ghost user from cache",
					"user_id", xmppUser.GetID(),
					"jid", xmppUser.GetJID())
			}
		}
	}

	if inactiveCount > 0 {
		m.logger.LogInfo("Completed inactive user cleanup cycle",
			"cached_users_checked", len(cachedUsers),
			"inactive_users_found", inactiveCount,
			"users_disconnected", disconnectedCount)
	} else {
		m.logger.LogDebug("No inactive users found during cleanup cycle", "cached_users_checked", len(cachedUsers))
	}
}

// createXMPPUserWithActivity creates an XMPP user with node-specific resource and activity data
func (m *UserManager) createXMPPUserWithActivity(id, displayName, userJID, password string, cfg *config.Configuration, log logger.Logger, lastActivity time.Time, enableLifecycle bool) *User {
	// Generate node-specific resource to prevent conflicts in HA environments
	baseResource := cfg.GetXMPPResource()
	nodeSpecificResource := fmt.Sprintf("%s-node-%s", baseResource, m.nodeID[:8])

	m.logger.LogDebug("Creating XMPP user with node-specific resource",
		"user_id", id,
		"base_resource", baseResource,
		"node_resource", nodeSpecificResource,
		"node_id", m.nodeID[:8])

	// Create TLS config based on certificate verification setting
	tlsConfig := &tls.Config{
		InsecureSkipVerify: cfg.XMPPInsecureSkipVerify, //nolint:gosec // Allow insecure TLS for testing environments
	}

	// Create XMPP client for this user with provided credentials and node-specific resource
	client := xmppClient.NewClientWithTLS(
		cfg.XMPPServerURL,
		userJID,
		password,             // Use the provided password (ghost password)
		nodeSpecificResource, // Use node-specific resource instead of base resource
		id,                   // Use user ID as remote ID
		tlsConfig,
		log,
	)

	ctx, cancel := context.WithCancel(context.Background())

	return &User{
		id:                   id,
		displayName:          displayName,
		jid:                  userJID,
		client:               client,
		state:                model.UserStateOffline,
		config:               cfg,
		ctx:                  ctx,
		cancel:               cancel,
		logger:               log,
		lastActivity:         lastActivity,    // Use provided activity time
		enableLifecycleCheck: enableLifecycle, // Use provided lifecycle setting
	}
}

// Cache management methods for connection caching

// getCachedUser retrieves a user from the connection cache
func (m *UserManager) getCachedUser(mattermostUserID string) (*User, bool) {
	m.activeUsersMu.RLock()
	defer m.activeUsersMu.RUnlock()
	user, exists := m.activeUsers[mattermostUserID]
	return user, exists
}

// cacheUser stores a user in the connection cache
func (m *UserManager) cacheUser(mattermostUserID string, user *User) {
	m.activeUsersMu.Lock()
	defer m.activeUsersMu.Unlock()
	m.activeUsers[mattermostUserID] = user
	m.logger.LogDebug("Cached user connection",
		"user_id", mattermostUserID,
		"ghost_jid", user.GetJID(),
		"cache_size", len(m.activeUsers))
}

// removeCachedUser removes a user from the connection cache
func (m *UserManager) removeCachedUser(mattermostUserID string) {
	m.activeUsersMu.Lock()
	defer m.activeUsersMu.Unlock()
	if user, exists := m.activeUsers[mattermostUserID]; exists {
		delete(m.activeUsers, mattermostUserID)
		m.logger.LogDebug("Removed user from cache",
			"user_id", mattermostUserID,
			"ghost_jid", user.GetJID(),
			"cache_size", len(m.activeUsers))
	}
}

// getCachedUsers returns all cached users (for lifecycle management)
func (m *UserManager) getCachedUsers() []*User {
	m.activeUsersMu.RLock()
	defer m.activeUsersMu.RUnlock()
	users := make([]*User, 0, len(m.activeUsers))
	for _, user := range m.activeUsers {
		users = append(users, user)
	}
	return users
}

func (m *UserManager) getCurrentTimestamp() int64 {
	// TODO: Use proper time source (time.Now().Unix())
	return 0
}
