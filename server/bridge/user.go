package bridge

import (
	"context"
	"fmt"
	"sync"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/config"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/logger"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/model"
)

// Manager implements the BridgeUserManager interface with bridge-agnostic logic
type UserManager struct {
	bridgeType string
	logger     logger.Logger
	users      map[string]model.BridgeUser
	mu         sync.RWMutex
	ctx        context.Context
	cancel     context.CancelFunc
}

// NewUserManager creates a new user manager for a specific bridge type
func NewUserManager(bridgeType string, logger logger.Logger) model.BridgeUserManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &UserManager{
		bridgeType: bridgeType,
		logger:     logger,
		users:      make(map[string]model.BridgeUser),
		ctx:        ctx,
		cancel:     cancel,
	}
}

// CreateUser adds a user to the bridge system
func (m *UserManager) CreateUser(user model.BridgeUser) error {
	// Validate the user first
	if err := user.Validate(); err != nil {
		return fmt.Errorf("invalid user: %w", err)
	}

	userID := user.GetID()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if user already exists
	if _, exists := m.users[userID]; exists {
		return fmt.Errorf("user %s already exists", userID)
	}

	m.logger.LogDebug("Adding bridge user", "bridge_type", m.bridgeType, "user_id", userID, "display_name", user.GetDisplayName())

	// Store the user
	m.users[userID] = user

	m.logger.LogInfo("Bridge user added successfully", "bridge_type", m.bridgeType, "user_id", userID)
	return nil
}

// GetUser retrieves a user by ID
func (m *UserManager) GetUser(userID string) (model.BridgeUser, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	user, exists := m.users[userID]
	if !exists {
		return nil, fmt.Errorf("user %s not found", userID)
	}

	return user, nil
}

// DeleteUser removes a user from the bridge system
func (m *UserManager) DeleteUser(userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	user, exists := m.users[userID]
	if !exists {
		return fmt.Errorf("user %s not found", userID)
	}

	m.logger.LogDebug("Deleting bridge user", "bridge_type", m.bridgeType, "user_id", userID)

	// Stop the user first
	if err := user.Stop(); err != nil {
		m.logger.LogWarn("Error stopping user during deletion", "bridge_type", m.bridgeType, "user_id", userID, "error", err)
	}

	// Disconnect if still connected
	if user.IsConnected() {
		if err := user.Disconnect(); err != nil {
			m.logger.LogWarn("Error disconnecting user during deletion", "bridge_type", m.bridgeType, "user_id", userID, "error", err)
		}
	}

	// Remove from map
	delete(m.users, userID)

	m.logger.LogInfo("Bridge user deleted successfully", "bridge_type", m.bridgeType, "user_id", userID)
	return nil
}

// ListUsers returns a list of all users
func (m *UserManager) ListUsers() []model.BridgeUser {
	m.mu.RLock()
	defer m.mu.RUnlock()

	users := make([]model.BridgeUser, 0, len(m.users))
	for _, user := range m.users {
		users = append(users, user)
	}

	return users
}

// HasUser checks if a user exists
func (m *UserManager) HasUser(userID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, exists := m.users[userID]
	return exists
}

// Start initializes the user manager
func (m *UserManager) Start(ctx context.Context) error {
	m.logger.LogDebug("Starting user manager", "bridge_type", m.bridgeType)

	// Update context
	m.ctx = ctx

	// Start all existing users
	m.mu.RLock()
	defer m.mu.RUnlock()

	for userID, user := range m.users {
		if err := user.Start(ctx); err != nil {
			m.logger.LogWarn("Failed to start user during manager startup", "bridge_type", m.bridgeType, "user_id", userID, "error", err)
			// Continue starting other users even if one fails
		}
	}

	m.logger.LogInfo("User manager started", "bridge_type", m.bridgeType, "user_count", len(m.users))
	return nil
}

// Stop shuts down the user manager
func (m *UserManager) Stop() error {
	m.logger.LogDebug("Stopping user manager", "bridge_type", m.bridgeType)

	if m.cancel != nil {
		m.cancel()
	}

	// Stop all users
	m.mu.RLock()
	users := make([]model.BridgeUser, 0, len(m.users))
	for _, user := range m.users {
		users = append(users, user)
	}
	m.mu.RUnlock()

	for _, user := range users {
		if err := user.Stop(); err != nil {
			m.logger.LogWarn("Error stopping user during manager shutdown", "bridge_type", m.bridgeType, "user_id", user.GetID(), "error", err)
		}
	}

	m.logger.LogInfo("User manager stopped", "bridge_type", m.bridgeType)
	return nil
}

// UpdateConfiguration updates configuration for all users
func (m *UserManager) UpdateConfiguration(cfg *config.Configuration) error {
	m.logger.LogDebug("Updating configuration for user manager", "bridge_type", m.bridgeType)

	// For now, we don't propagate config changes to individual users
	// This can be extended later if needed
	m.logger.LogInfo("User manager configuration updated", "bridge_type", m.bridgeType)
	return nil
}

// GetBridgeType returns the bridge type this manager handles
func (m *UserManager) GetBridgeType() string {
	return m.bridgeType
}
