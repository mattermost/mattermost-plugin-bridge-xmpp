package main

import (
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/logger"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/store/kvstore"
)

// testLogger implements logger.Logger for tests.
type testLogger struct {
	t *testing.T
}

func newTestLogger(t *testing.T) logger.Logger {
	return &testLogger{t: t}
}

func (l *testLogger) LogDebug(message string, keyValuePairs ...any) {
	if l.t != nil {
		l.t.Logf("[DEBUG] %s %v", message, keyValuePairs)
	}
}

func (l *testLogger) LogInfo(message string, keyValuePairs ...any) {
	if l.t != nil {
		l.t.Logf("[INFO] %s %v", message, keyValuePairs)
	}
}

func (l *testLogger) LogWarn(message string, keyValuePairs ...any) {
	if l.t != nil {
		l.t.Logf("[WARN] %s %v", message, keyValuePairs)
	}
}

func (l *testLogger) LogError(message string, keyValuePairs ...any) {
	if l.t != nil {
		l.t.Logf("[ERROR] %s %v", message, keyValuePairs)
	}
}

// MemoryKVStore is an in-memory kvstore.KVStore for tests.
type MemoryKVStore struct {
	data map[string][]byte
	mu   sync.RWMutex
}

// NewMemoryKVStore creates an empty in-memory KV store.
func NewMemoryKVStore() kvstore.KVStore {
	return &MemoryKVStore{
		data: make(map[string][]byte),
	}
}

func (m *MemoryKVStore) GetTemplateData(userID string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := "template_key-" + userID
	if data, exists := m.data[key]; exists {
		return string(data), nil
	}
	return "", errors.New("key not found")
}

func (m *MemoryKVStore) Get(key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if data, exists := m.data[key]; exists {
		result := make([]byte, len(data))
		copy(result, data)
		return result, nil
	}
	return nil, errors.New("key not found")
}

func (m *MemoryKVStore) Set(key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data := make([]byte, len(value))
	copy(data, value)
	m.data[key] = data
	return nil
}

func (m *MemoryKVStore) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.data, key)
	return nil
}

func (m *MemoryKVStore) ListKeys(page, perPage int) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	keys := make([]string, 0, len(m.data))
	for key := range m.data {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	start := page * perPage
	if start >= len(keys) {
		return []string{}, nil
	}
	end := min(start+perPage, len(keys))
	return keys[start:end], nil
}

func (m *MemoryKVStore) ListKeysWithPrefix(page, perPage int, prefix string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	keys := make([]string, 0, len(m.data))
	for key := range m.data {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	start := page * perPage
	if start >= len(keys) {
		return []string{}, nil
	}
	end := min(start+perPage, len(keys))
	return keys[start:end], nil
}

func TestMemoryKVStore(t *testing.T) {
	store := NewMemoryKVStore()

	require.NoError(t, store.Set("test-key", []byte("test-value")))

	value, err := store.Get("test-key")
	require.NoError(t, err)
	require.Equal(t, "test-value", string(value))

	_, err = store.Get("missing")
	require.Error(t, err)

	require.NoError(t, store.Delete("test-key"))
	_, err = store.Get("test-key")
	require.Error(t, err)
}
