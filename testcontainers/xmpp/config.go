// Package xmpp provides Prosody testcontainer helpers for XMPP bridge tests.
package xmpp

// TestConfig holds credentials and room identifiers for a Prosody test instance.
type TestConfig struct {
	BridgeJID        string
	BridgePassword   string
	OccupantJID      string
	OccupantPassword string
	RoomJID          string
	Domain           string
}

// DefaultConfig returns the standard Prosody test credentials and room.
func DefaultConfig() TestConfig {
	return TestConfig{
		BridgeJID:        "bridge@localhost",
		BridgePassword:   "bridgepass",
		OccupantJID:      "occupant@localhost",
		OccupantPassword: "occupantpass",
		RoomJID:          "syncroom@conference.localhost",
		Domain:           "localhost",
	}
}
