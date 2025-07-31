package config

import (
	"fmt"
	"strings"
)

const DefaultXMPPUsernamePrefix = "xmpp"

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
	XMPPServerURL               string `json:"XMPPServerURL"`
	XMPPUsername                string `json:"XMPPUsername"`
	XMPPPassword                string `json:"XMPPPassword"`
	EnableSync                  bool   `json:"EnableSync"`
	XMPPUsernamePrefix          string `json:"XMPPUsernamePrefix"`
	XMPPResource                string `json:"XMPPResource"`
	XMPPInsecureSkipVerify      bool   `json:"XMPPInsecureSkipVerify"`
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
		c.XMPPUsernamePrefix == other.XMPPUsernamePrefix &&
		c.XMPPResource == other.XMPPResource &&
		c.XMPPInsecureSkipVerify == other.XMPPInsecureSkipVerify
}

// Clone shallow copies the configuration. Your implementation may require a deep copy if
// your configuration has reference types.
func (c *Configuration) Clone() *Configuration {
	var clone = *c
	return &clone
}

// GetXMPPUsernamePrefix returns the configured username prefix, or the default if not set
func (c *Configuration) GetXMPPUsernamePrefix() string {
	if c.XMPPUsernamePrefix == "" {
		return DefaultXMPPUsernamePrefix
	}
	return c.XMPPUsernamePrefix
}

// GetXMPPResource returns the configured XMPP resource, or a default if not set
func (c *Configuration) GetXMPPResource() string {
	if c.XMPPResource == "" {
		return "mattermost-bridge"
	}
	return c.XMPPResource
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

		// Validate username prefix doesn't contain invalid characters
		prefix := c.GetXMPPUsernamePrefix()
		if strings.ContainsAny(prefix, ":@/\\") {
			return fmt.Errorf("XMPP Username Prefix cannot contain special characters (:, @, /, \\)")
		}
	}

	return nil
}