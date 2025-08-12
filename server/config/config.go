package config

import (
	"fmt"
	"strings"
)

// Configuration captures the plugin's external configuration as exposed in the Mattermost server
// configuration, as well as values computed from the configuration. Any public fields will be
// deserialized from the Mattermost server configuration in OnConfigurationChange.
//
// As plugins are inherently concurrent (hooks being called asynchronously), and the plugin
// configuration can change at any time, access to the configuration must be synchronized. The
// strategy used in this plugin is to guard a pointer to the configuration, and clone the entire
// struct whenever it changes. You may replace this with whatever strategy you choose.
//
// If you add non-reference types to your configuration struct, be sure to rewrite Clone as a deep
// copy appropriate for your types.
type Configuration struct {
	XMPPServerURL          string `json:"XMPPServerURL"`
	XMPPUsername           string `json:"XMPPUsername"`
	XMPPPassword           string `json:"XMPPPassword"`
	EnableSync             bool   `json:"EnableSync"`
	XMPPResource           string `json:"XMPPResource"`
	XMPPInsecureSkipVerify bool   `json:"XMPPInsecureSkipVerify"`

	// Ghost User Settings (XEP-0077 In-Band Registration)
	EnableXMPPGhostUsers bool   `json:"EnableXMPPGhostUsers"`
	XMPPGhostUserPrefix  string `json:"XMPPGhostUserPrefix"`
	XMPPGhostUserDomain  string `json:"XMPPGhostUserDomain"`
	XMPPGhostUserCleanup bool   `json:"XMPPGhostUserCleanup"`
}

// Equals compares two configuration structs
func (c *Configuration) Equals(other *Configuration) bool {
	if c == nil && other == nil {
		return true
	}
	if c == nil || other == nil {
		return false
	}

	return c.XMPPServerURL == other.XMPPServerURL &&
		c.XMPPUsername == other.XMPPUsername &&
		c.XMPPPassword == other.XMPPPassword &&
		c.EnableSync == other.EnableSync &&
		c.XMPPResource == other.XMPPResource &&
		c.XMPPInsecureSkipVerify == other.XMPPInsecureSkipVerify &&
		c.EnableXMPPGhostUsers == other.EnableXMPPGhostUsers &&
		c.XMPPGhostUserPrefix == other.XMPPGhostUserPrefix &&
		c.XMPPGhostUserDomain == other.XMPPGhostUserDomain &&
		c.XMPPGhostUserCleanup == other.XMPPGhostUserCleanup
}

// Clone shallow copies the configuration. Your implementation may require a deep copy if
// your configuration has reference types.
func (c *Configuration) Clone() *Configuration {
	var clone = *c
	return &clone
}

// GetXMPPResource returns the configured XMPP resource, or a default if not set
func (c *Configuration) GetXMPPResource() string {
	if c.XMPPResource == "" {
		return "mattermost-bridge"
	}
	return c.XMPPResource
}

// GetXMPPGhostUserDomain returns the configured ghost user domain, or derives it from server URL
func (c *Configuration) GetXMPPGhostUserDomain() string {
	if c.XMPPGhostUserDomain != "" {
		return c.XMPPGhostUserDomain
	}

	// Extract domain from bridge username as fallback
	if c.XMPPUsername != "" && strings.Contains(c.XMPPUsername, "@") {
		parts := strings.Split(c.XMPPUsername, "@")
		if len(parts) > 1 {
			return parts[1]
		}
	}

	// Last resort: try to extract from server URL
	if c.XMPPServerURL != "" {
		parts := strings.Split(c.XMPPServerURL, ":")
		if len(parts) > 0 {
			return parts[0]
		}
	}

	return "localhost"
}

// IsGhostUserEnabled returns true if ghost users should be created
func (c *Configuration) IsGhostUserEnabled() bool {
	return c.EnableXMPPGhostUsers && c.EnableSync
}

// IsGhostUserCleanupEnabled returns true if ghost users should be cleaned up on removal
func (c *Configuration) IsGhostUserCleanupEnabled() bool {
	return c.XMPPGhostUserCleanup
}

// IsValid validates the configuration and returns an error if invalid
func (c *Configuration) IsValid() error {
	if c.EnableSync {
		if c.XMPPServerURL == "" {
			return fmt.Errorf("XMPP Server URL is required when sync is enabled")
		}
		if c.XMPPUsername == "" {
			return fmt.Errorf("XMPP Username is required when sync is enabled")
		}
		if c.XMPPPassword == "" {
			return fmt.Errorf("XMPP Password is required when sync is enabled")
		}

		// Validate server URL format
		if !strings.Contains(c.XMPPServerURL, ":") {
			return fmt.Errorf("XMPP Server URL must include port (e.g., server.com:5222)")
		}

		// Validate ghost user configuration if enabled
		if c.EnableXMPPGhostUsers {
			if c.XMPPGhostUserPrefix == "" {
				return fmt.Errorf("XMPP Ghost User Prefix is required when ghost users are enabled")
			}

			// Validate ghost user prefix doesn't contain invalid characters
			if strings.ContainsAny(c.XMPPGhostUserPrefix, ":@/\\") {
				return fmt.Errorf("XMPP Ghost User Prefix cannot contain special characters (:, @, /, \\)")
			}
		}
	}

	return nil
}
