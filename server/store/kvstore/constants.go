package kvstore

import "strings"

// KV Store key prefixes and constants
// This file centralizes all KV store key patterns used throughout the plugin
// to ensure consistency and avoid key conflicts.

const (
	// CurrentKVStoreVersion is the current version requiring migrations
	CurrentKVStoreVersion = 2
	// KeyPrefixXMPPUser is the prefix for XMPP user ID -> Mattermost user ID mappings
	KeyPrefixXMPPUser = "xmpp_user_"
	// KeyPrefixMattermostUser is the prefix for Mattermost user ID -> XMPP user ID mappings
	KeyPrefixMattermostUser = "mattermost_user_"

	// KeyPrefixChannelMap is the prefix for bridge-agnostic channel mappings
	KeyPrefixChannelMap = "channel_map_"

	// KeyPrefixGhostUser is the prefix for Mattermost user ID -> XMPP ghost user ID cache
	KeyPrefixGhostUser = "ghost_user_"
	// KeyPrefixGhostRoom is the prefix for ghost user room membership tracking
	KeyPrefixGhostRoom = "ghost_room_"

	// KeyPrefixXMPPEventPost is the prefix for XMPP event ID -> Mattermost post ID mappings
	KeyPrefixXMPPEventPost = "xmpp_event_post_"
	// KeyPrefixXMPPReaction is the prefix for XMPP reaction event ID -> reaction info mappings
	KeyPrefixXMPPReaction = "xmpp_reaction_"

	// KeyStoreVersion is the key for tracking the current KV store schema version
	KeyStoreVersion = "kv_store_version"

	// KeyPrefixLegacyDMMapping was the old prefix for DM mappings
	KeyPrefixLegacyDMMapping = "dm_mapping_"
	// KeyPrefixLegacyXMPPDMMapping was the old prefix for XMPP DM mappings
	KeyPrefixLegacyXMPPDMMapping = "xmpp_dm_mapping_"
)

// Helper functions for building KV store keys

// BuildXMPPUserKey creates a key for XMPP user -> Mattermost user mapping
func BuildXMPPUserKey(xmppUserID string) string {
	return KeyPrefixXMPPUser + xmppUserID
}

// BuildMattermostUserKey creates a key for Mattermost user -> XMPP user mapping
func BuildMattermostUserKey(mattermostUserID string) string {
	return KeyPrefixMattermostUser + mattermostUserID
}

// BuildChannelMapKey creates a bridge-agnostic key for channel mappings
func BuildChannelMapKey(bridgeName, identifier string) string {
	return KeyPrefixChannelMap + bridgeName + "_" + identifier
}

// BuildGhostUserKey creates a key for ghost user cache
func BuildGhostUserKey(mattermostUserID string) string {
	return KeyPrefixGhostUser + mattermostUserID
}

// BuildGhostRoomKey creates a key for ghost user room membership
func BuildGhostRoomKey(mattermostUserID, roomID string) string {
	return KeyPrefixGhostRoom + mattermostUserID + "_" + roomID
}

// BuildXMPPEventPostKey creates a key for XMPP event -> post mapping
func BuildXMPPEventPostKey(xmppEventID string) string {
	return KeyPrefixXMPPEventPost + xmppEventID
}

// BuildXMPPReactionKey creates a key for XMPP reaction storage
func BuildXMPPReactionKey(reactionEventID string) string {
	return KeyPrefixXMPPReaction + reactionEventID
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
