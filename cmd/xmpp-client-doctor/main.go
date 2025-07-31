package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/xmpp"
)

const (
	// Default values for development server (sidecar)
	defaultServer   = "localhost:5222"
	defaultUsername = "testuser@localhost"
	defaultPassword = "testpass"
	defaultResource = "doctor"
)

type Config struct {
	Server             string
	Username           string
	Password           string
	Resource           string
	Verbose            bool
	InsecureSkipVerify bool
}

func main() {
	config := &Config{}

	// Define command line flags
	flag.StringVar(&config.Server, "server", defaultServer, "XMPP server address (host:port)")
	flag.StringVar(&config.Username, "username", defaultUsername, "XMPP username/JID")
	flag.StringVar(&config.Password, "password", defaultPassword, "XMPP password")
	flag.StringVar(&config.Resource, "resource", defaultResource, "XMPP resource")
	flag.BoolVar(&config.Verbose, "verbose", true, "Enable verbose logging")
	flag.BoolVar(&config.InsecureSkipVerify, "insecure-skip-verify", true, "Skip TLS certificate verification (for development)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "xmpp-client-doctor - Test XMPP client connectivity\n\n")
		fmt.Fprintf(os.Stderr, "This tool tests the XMPP client implementation by connecting to an XMPP server,\n")
		fmt.Fprintf(os.Stderr, "performing a connection test, and then disconnecting gracefully.\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  %s [flags]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nDefault values are configured for the development server in ./sidecar/\n")
		fmt.Fprintf(os.Stderr, "Make sure to start the development server with: cd sidecar && docker-compose up -d\n")
	}

	flag.Parse()

	if config.Verbose {
		log.SetFlags(log.LstdFlags | log.Lmicroseconds)
		log.Printf("Starting XMPP client doctor...")
		log.Printf("Configuration:")
		log.Printf("  Server: %s", config.Server)
		log.Printf("  Username: %s", config.Username)
		log.Printf("  Resource: %s", config.Resource)
		log.Printf("  Password: %s", maskPassword(config.Password))
	}

	// Test the XMPP client
	if err := testXMPPClient(config); err != nil {
		log.Fatalf("❌ XMPP client test failed: %v", err)
	}

	if config.Verbose {
		log.Printf("✅ XMPP client test completed successfully!")
	} else {
		fmt.Println("✅ XMPP client connectivity test passed!")
	}
}

func testXMPPClient(config *Config) error {
	if config.Verbose {
		log.Printf("Creating XMPP client...")
	}

	// Create XMPP client with optional TLS configuration
	var client *xmpp.Client
	if config.InsecureSkipVerify {
		if config.Verbose {
			log.Printf("Using insecure TLS configuration (skipping certificate verification)")
		}
		tlsConfig := &tls.Config{
			InsecureSkipVerify: true,
		}
		client = xmpp.NewClientWithTLS(
			config.Server,
			config.Username,
			config.Password,
			config.Resource,
			"doctor-remote-id",
			tlsConfig,
		)
	} else {
		client = xmpp.NewClient(
			config.Server,
			config.Username,
			config.Password,
			config.Resource,
			"doctor-remote-id",
		)
	}

	if config.Verbose {
		log.Printf("Attempting to connect to XMPP server...")
	}

	// Test connection
	start := time.Now()
	err := client.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to XMPP server: %w", err)
	}
	connectDuration := time.Since(start)

	if config.Verbose {
		log.Printf("✅ Connected to XMPP server in %v", connectDuration)
		log.Printf("Testing connection health...")
	}

	// Test connection health
	start = time.Now()
	err = client.TestConnection()
	if err != nil {
		return fmt.Errorf("connection health test failed: %w", err)
	}
	pingDuration := time.Since(start)

	if config.Verbose {
		log.Printf("✅ Connection health test passed in %v", pingDuration)
		log.Printf("Disconnecting from XMPP server...")
	}

	// Disconnect
	start = time.Now()
	err = client.Disconnect()
	if err != nil {
		return fmt.Errorf("failed to disconnect from XMPP server: %w", err)
	}
	disconnectDuration := time.Since(start)

	if config.Verbose {
		log.Printf("✅ Disconnected from XMPP server in %v", disconnectDuration)
		log.Printf("Connection summary:")
		log.Printf("  Connect time: %v", connectDuration)
		log.Printf("  Ping time: %v", pingDuration)
		log.Printf("  Disconnect time: %v", disconnectDuration)
		log.Printf("  Total time: %v", connectDuration+pingDuration+disconnectDuration)
	}

	return nil
}

func maskPassword(password string) string {
	if len(password) <= 2 {
		return "****"
	}
	return password[:2] + "****"
}