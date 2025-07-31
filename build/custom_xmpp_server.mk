# XMPP Development Server Management
# This file provides targets for managing the development XMPP server

# XMPP server configuration
XMPP_COMPOSE_FILE := sidecar/docker-compose.yml
XMPP_ADMIN_URL := http://localhost:9090
XMPP_SERVER_HOST := localhost
XMPP_SERVER_PORT := 5222

.PHONY: devserver_start devserver_stop devserver_status devserver_logs devserver_clean devserver_doctor devserver_help

## Start the XMPP development server
devserver_start:
	@echo "Starting XMPP development server..."
	@if [ ! -f "$(XMPP_COMPOSE_FILE)" ]; then \
		echo "Error: $(XMPP_COMPOSE_FILE) not found"; \
		exit 1; \
	fi
	@cd sidecar && docker compose up -d
	@echo "✅ XMPP server started successfully"
	@echo "Admin console: $(XMPP_ADMIN_URL)"
	@echo "XMPP server: $(XMPP_SERVER_HOST):$(XMPP_SERVER_PORT)"
	@echo ""
	@echo "See sidecar/README.md for setup instructions"

## Stop the XMPP development server
devserver_stop:
	@echo "Stopping XMPP development server..."
	@cd sidecar && docker compose down
	@echo "✅ XMPP server stopped"

## Check status of XMPP development server
devserver_status:
	@echo "XMPP Development Server Status:"
	@cd sidecar && docker compose ps

## View XMPP server logs
devserver_logs:
	@echo "XMPP Server Logs (press Ctrl+C to exit):"
	@cd sidecar && docker compose logs -f openfire

## Clean up XMPP server data and containers
devserver_clean:
	@echo "⚠️  This will remove all XMPP server data and containers!"
	@read -p "Are you sure? (y/N): " confirm && [ "$$confirm" = "y" ] || exit 1
	@echo "Cleaning up XMPP development server..."
	@cd sidecar && docker compose down -v --remove-orphans
	@docker system prune -f --filter label=com.docker.compose.project=sidecar
	@echo "✅ XMPP server cleaned up"

## Test XMPP client connectivity with doctor tool (insecure for development)
devserver_doctor:
	@echo "Testing XMPP client connectivity..."
	@go run cmd/xmpp-client-doctor/main.go -insecure-skip-verify=true -verbose=true

## Show XMPP development server help
devserver_help:
	@echo "XMPP Development Server Targets:"
	@echo ""
	@echo "  devserver_start   - Start the XMPP development server"
	@echo "  devserver_stop    - Stop the XMPP development server"
	@echo "  devserver_status  - Check server status"
	@echo "  devserver_logs    - View server logs"
	@echo "  devserver_clean   - Clean up server data and containers"
	@echo "  devserver_doctor  - Test XMPP client connectivity"
	@echo "  devserver_help    - Show this help message"
	@echo ""
	@echo "Server Details:"
	@echo "  Admin Console: $(XMPP_ADMIN_URL)"
	@echo "  XMPP Server:   $(XMPP_SERVER_HOST):$(XMPP_SERVER_PORT)"
	@echo ""
	@echo "See sidecar/README.md for detailed setup instructions"