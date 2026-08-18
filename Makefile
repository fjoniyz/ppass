.PHONY: all build test run-db delete-db run-second-service test-second-service delete-second-service run-api-service test-api-service delete-api-service list logs clean help

BINARY=paas
DB_CONFIG=config_files/databases/user_db_config.yaml
SECOND_CONFIG=config_files/python/second_service_config.yaml
API_CONFIG=config_files/python/api_service_config.yaml
ENVOY_IP=10.0.0.1

all: build

help:
	@echo "Available Makefile commands:"
	@echo "  make build                     - Build the paas binary"
	@echo "  make test                      - Run all unit tests"
	@echo "  make run-db                    - Start user-db database"
	@echo "  make delete-db                 - Delete user-db and release resources"
	@echo "  make run-second-service        - Start second-service (port 8081, domain second.local)"
	@echo "  make test-second-service       - Test second-service via Envoy and directly"
	@echo "  make delete-second-service     - Delete second-service and release resources"
	@echo "  make run-api-service           - Start api-service (port 8082, domain api.local)"
	@echo "  make test-api-service          - Test api-service via Envoy and directly"
	@echo "  make delete-api-service        - Delete api-service and release resources"
	@echo "  make list                      - List all deployed services and databases"
	@echo "  make logs                      - Tail logs for all services and Envoy"
	@echo "  make clean                     - Clean binary and hanging network interfaces"

build:
	@echo "==> Building $(BINARY)..."
	go build -o $(BINARY) .

test:
	@echo "==> Running unit tests..."
	go test -v ./...

# database
run-db: build
	@echo "==> Starting database (user-db)..."
	sudo ./$(BINARY) create $(DB_CONFIG)

delete-db:
	@echo "==> Deleting database (user-db)..."
	sudo ./$(BINARY) delete database user-db

# second-service
run-second-service: build
	@echo "==> Starting second-service..."
	sudo ./$(BINARY) create $(SECOND_CONFIG)

test-second-service-lb:
	@echo "==> Testing second-service via Envoy (http://$(ENVOY_IP):8081 [Host: second.local])..."
	curl -i -H "Host: second.local" http://$(ENVOY_IP):8081/

test-second-service-direct:
	@echo "==> Testing second-service directly (http://10.0.0.2:8888)..."
	curl -i http://10.0.0.2:8888/

test-second-service: test-second-service-lb test-second-service-direct

delete-second-service:
	@echo "==> Deleting second-service..."
	sudo ./$(BINARY) delete service second-service

# api-service
run-api-service: build
	@echo "==> Starting api-service..."
	sudo ./$(BINARY) create $(API_CONFIG)

test-api-service-lb:
	@echo "==> Testing api-service via Envoy (http://$(ENVOY_IP):8082 [Host: api.local])..."
	curl -i -H "Host: api.local" http://$(ENVOY_IP):8082/

test-api-service-direct:
	@echo "==> Testing api-service directly (http://10.0.0.3:8888)..."
	curl -i http://10.0.0.3:8888/

test-api-service: test-api-service-lb test-api-service-direct

delete-api-service:
	@echo "==> Deleting api-service..."
	sudo ./$(BINARY) delete service api-service

list:
	@echo "==> Listing active workloads..."
	sudo ./$(BINARY) list

logs:
	@echo "==> Tailing logs (Ctrl+C to stop)..."
	sudo tail -f /var/log/*.log

clean:
	@echo "==> Cleaning up..."
	@if [ -f $(BINARY) ]; then rm -f $(BINARY); fi
	@if [ -f ./scripts/clean_interfaces.sh ]; then sudo ./scripts/clean_interfaces.sh; fi
