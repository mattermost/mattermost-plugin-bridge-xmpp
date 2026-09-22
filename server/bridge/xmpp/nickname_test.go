package xmpp

import "testing"

func TestGhostNicknameRoundTrip(t *testing.T) {
	// Loop detection depends on every nickname ghostNickname produces being matched
	// by isGhostNickname. If these two ever drift, the bridge stops recognising its
	// own messages and echoes every Mattermost post back as a duplicate.
	for _, username := range []string{"felipe", "test2", "a", "user with spaces", "mm_felipe"} {
		if nick := ghostNickname(username); !isGhostNickname(nick) {
			t.Errorf("ghostNickname(%q) = %q, not matched by isGhostNickname", username, nick)
		}
	}
}

func TestIsGhostNickname(t *testing.T) {
	for nick, want := range map[string]bool{
		"felipe (Mattermost)":           true,
		"test2":                         false,
		"testuser":                      false,
		"mm_y51hdyyn1bnhib1dnxxi8zykdh": false,
		"(Mattermost) felipe":           false,
		"":                              false,
	} {
		if got := isGhostNickname(nick); got != want {
			t.Errorf("isGhostNickname(%q) = %v, want %v", nick, got, want)
		}
	}
}
