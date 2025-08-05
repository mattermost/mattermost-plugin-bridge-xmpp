package kvstore

import "strings"

// KV Store key prefixes and constants
// This file centralizes all KV store key patterns used throughout the plugin
// to ensure consistency and avoid key conflicts.

const (
	// KeyPrefixChannelMap is the prefix for bridge-agnostic channel mappings
	KeyPrefixChannelMap = "channel_map_"
)

// Helper functions for building KV store keys

// BuildChannelMapKey creates a bridge-agnostic key for channel mappings
func BuildChannelMapKey(bridgeName, identifier string) string {
	return KeyPrefixChannelMap + bridgeName + "_" + identifier
}

// ExtractIdentifierFromChannelMapKey extracts the identifier from a bridge-agnostic channel map key
func ExtractIdentifierFromChannelMapKey(key, bridgeName string) string {
	expectedPrefix := KeyPrefixChannelMap + bridgeName + "_"
	if len(key) <= len(expectedPrefix) {
		return ""
	}
	if !strings.HasPrefix(key, expectedPrefix) {
		return ""
	}
	return key[len(expectedPrefix):]
}