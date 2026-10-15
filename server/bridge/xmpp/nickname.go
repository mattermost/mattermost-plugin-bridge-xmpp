package xmpp

import "strings"

// ghostNicknameSuffix marks a MUC occupant as a ghost user driven by this bridge.
//
// A ghost's JID is built from the Mattermost user ID, which makes for an unreadable
// occupant list, so ghosts join rooms under the Mattermost username instead. The
// suffix keeps them attributable, and is the only thing distinguishing a ghost from
// a native XMPP participant once the ID is gone: loop detection matches on it, so
// the nickname is built and recognised here and nowhere else.
const ghostNicknameSuffix = " (Mattermost)"

// ghostNickname returns the MUC nickname a ghost user joins rooms under.
func ghostNickname(username string) string {
	return username + ghostNicknameSuffix
}

// isGhostNickname reports whether a MUC nickname belongs to one of our ghost users.
func isGhostNickname(nickname string) bool {
	return strings.HasSuffix(nickname, ghostNicknameSuffix)
}
