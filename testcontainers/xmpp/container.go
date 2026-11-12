package xmpp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const c2sPort = "5222/tcp"

// Container wraps a Prosody testcontainer reachable from the host.
type Container struct {
	Container testcontainers.Container
	HostURL   string // host:port for clients on the host
	Config    TestConfig
}

// StartContainer builds and starts Prosody from the package fixtures Dockerfile.
func StartContainer(t testing.TB, cfg TestConfig) *Container {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	fixtures, err := fixturesDir()
	require.NoError(t, err)

	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    fixtures,
			Dockerfile: "Dockerfile",
		},
		ExposedPorts: []string{c2sPort},
		WaitingFor:   wait.ForListeningPort(c2sPort).WithStartupTimeout(2 * time.Minute),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err)

	host, err := container.Host(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		require.NoError(t, err)
	}
	port, err := container.MappedPort(ctx, c2sPort)
	if err != nil {
		_ = container.Terminate(ctx)
		require.NoError(t, err)
	}

	return &Container{
		Container: container,
		HostURL:   fmt.Sprintf("%s:%s", host, port.Port()),
		Config:    cfg,
	}
}

// Cleanup terminates the Prosody container.
func (c *Container) Cleanup(t testing.TB) {
	t.Helper()

	if c == nil || c.Container == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := c.Container.Terminate(ctx); err != nil {
		t.Logf("Warning: Failed to terminate Prosody container: %v", err)
	}
}

// fixturesDir locates testcontainers/xmpp/fixtures relative to this source file,
// falling back to walking parents from the working directory.
func fixturesDir() (string, error) {
	if _, thisFile, _, ok := runtime.Caller(0); ok {
		candidate := filepath.Join(filepath.Dir(thisFile), "fixtures")
		if hasDockerfile(candidate) {
			return candidate, nil
		}
	}

	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for {
		candidate := filepath.Join(dir, "testcontainers", "xmpp", "fixtures")
		if hasDockerfile(candidate) {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find testcontainers/xmpp/fixtures from %s", wd)
		}
		dir = parent
	}
}

func hasDockerfile(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "Dockerfile"))
	return err == nil
}
