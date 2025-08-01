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
	defaultTestRoom = "test1@conference.localhost"
)

type Config struct {
	Server             string
	Username           string
	Password           string
	Resource           string
	TestRoom           string
	TestMUC            bool
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
	flag.StringVar(&config.TestRoom, "test-room", defaultTestRoom, "MUC room JID for testing")
	flag.BoolVar(&config.TestMUC, "test-muc", true, "Enable MUC room testing (join/wait/leave)")
	flag.BoolVar(&config.Verbose, "verbose", true, "Enable verbose logging")
	flag.BoolVar(&config.InsecureSkipVerify, "insecure-skip-verify", true, "Skip TLS certificate verification (for development)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "xmpp-client-doctor - Test XMPP client connectivity and MUC operations\n\n")
		fmt.Fprintf(os.Stderr, "This tool tests the XMPP client implementation by connecting to an XMPP server,\n")
		fmt.Fprintf(os.Stderr, "performing connection tests, optionally testing MUC room operations,\n")
		fmt.Fprintf(os.Stderr, "and then disconnecting gracefully.\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  %s [flags]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  %s                    # Test basic connectivity\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s --test-muc         # Test connectivity and MUC operations\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s --test-muc=false   # Test connectivity only\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nDefault values are configured for the development server in ./sidecar/\n")
		fmt.Fprintf(os.Stderr, "Make sure to start the development server with: cd sidecar && docker-compose up -d\n")
		fmt.Fprintf(os.Stderr, "For MUC testing, create the test room 'test1' via the admin console at http://localhost:9090\n")
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
		if config.TestMUC {
			log.Printf("  Test Room: %s", config.TestRoom)
		}
	}

	// Test the XMPP client
	if err := testXMPPClient(config); err != nil {
		log.Fatalf("❌ XMPP client test failed: %v", err)
	}

	if config.Verbose {
		log.Printf("✅ XMPP client test completed successfully!")
	} else {
		fmt.Println("✅ XMPP client connectivity test passed!")
		if config.TestMUC {
			fmt.Println("✅ XMPP MUC operations test passed!")
		}
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
	}

	var mucDuration time.Duration
	
	// Test MUC operations if requested
	if config.TestMUC {
		start = time.Now()
		err = testMUCOperations(client, config)
		if err != nil {
			return fmt.Errorf("MUC operations test failed: %w", err)
		}
		mucDuration = time.Since(start)
	}

	if config.Verbose {
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
		if config.TestMUC {
			log.Printf("  MUC operations time: %v", mucDuration)
		}
		log.Printf("  Disconnect time: %v", disconnectDuration)
		totalTime := connectDuration + pingDuration + disconnectDuration
		if config.TestMUC {
			totalTime += mucDuration
		}
		log.Printf("  Total time: %v", totalTime)
	}

	return nil
}

func testMUCOperations(client *xmpp.Client, config *Config) error {
	if config.Verbose {
		log.Printf("Testing MUC operations with room: %s", config.TestRoom)
		log.Printf("Attempting to join MUC room...")
	}

	// Test joining the room
	start := time.Now()
	err := client.JoinRoom(config.TestRoom)
	if err != nil {
		return fmt.Errorf("failed to join MUC room %s: %w", config.TestRoom, err)
	}
	joinDuration := time.Since(start)
	
	var sendDuration time.Duration

	if config.Verbose {
		log.Printf("✅ Successfully joined MUC room in %v", joinDuration)
		log.Printf("Sending test message to room...")
	}

	// Send a test message
	testMessage := fmt.Sprintf("Test message from XMPP doctor at %s", time.Now().Format("15:04:05"))
	messageReq := xmpp.MessageRequest{
		RoomJID: config.TestRoom,
		Message: testMessage,
	}
	
	start = time.Now()
	_, err = client.SendMessage(messageReq)
	if err != nil {
		return fmt.Errorf("failed to send test message to room %s: %w", config.TestRoom, err)
	}
	sendDuration = time.Since(start)

	if config.Verbose {
		log.Printf("✅ Successfully sent message in %v", sendDuration)
		log.Printf("Message: %s", testMessage)
		log.Printf("Waiting 5 seconds in the room...")
	}

	// Wait 5 seconds
	time.Sleep(5 * time.Second)

	if config.Verbose {
		log.Printf("Attempting to leave MUC room...")
	}

	// Test leaving the room
	start = time.Now()
	err = client.LeaveRoom(config.TestRoom)
	if err != nil {
		return fmt.Errorf("failed to leave MUC room %s: %w", config.TestRoom, err)
	}
	leaveDuration := time.Since(start)

	if config.Verbose {
		log.Printf("✅ Successfully left MUC room in %v", leaveDuration)
		log.Printf("MUC operations summary:")
		log.Printf("  Join time: %v", joinDuration)
		log.Printf("  Send message time: %v", sendDuration)
		log.Printf("  Wait time: 5s")
		log.Printf("  Leave time: %v", leaveDuration)
		log.Printf("  Total MUC time: %v", joinDuration+sendDuration+5*time.Second+leaveDuration)
	}

	return nil
}

func maskPassword(password string) string {
	if len(password) <= 2 {
		return "****"
	}
	return password[:2] + "****"
}