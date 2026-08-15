.PHONY: all build run-second-service test-second-service test-second-service-direct test-second-service-lb delete-second-service list logs clean help

BINARY=paas
CONFIG=config_files/python/second_service_config.yaml
ENVOY_IP=10.0.0.1
ENVOY_PORT=8081
DOMAIN=second.local
SERVICE_IP=10.0.0.2
SERVICE_PORT=8888

all: build

help:
	@echo "Available Makefile commands:"
	@echo "  make build                     - Build the paas binary"
	@echo "  make run-second-service        - Build paas and launch second-service"
	@echo "  make test-second-service       - Test service via Envoy and directly"
	@echo "  make test-second-service-lb    - Send curl request through Envoy (10.0.0.1:8081)"
	@echo "  make test-second-service-direct- Send curl request directly to service (10.0.0.2:8888)"
	@echo "  make delete-second-service     - Delete second-service and release resources"
	@echo "  make list                      - List all deployed services and databases"
	@echo "  make logs                      - Tail logs for second-service and Envoy"
	@echo "  make clean                     - Clean binary and hanging network interfaces"

build:
	@echo "==> Building $(BINARY)..."
	go build -o $(BINARY) .

run-second-service: build
	@echo "==> Starting second-service..."
	sudo ./$(BINARY) create $(CONFIG)

test-second-service-lb:
	@echo "==> Testing via Envoy Load Balancer (http://$(ENVOY_IP):$(ENVOY_PORT) [Host: $(DOMAIN)])..."
	curl -i -H "Host: $(DOMAIN)" http://$(ENVOY_IP):$(ENVOY_PORT)/

test-second-service-direct:
	@echo "==> Testing directly (http://$(SERVICE_IP):$(SERVICE_PORT))..."
	curl -i http://$(SERVICE_IP):$(SERVICE_PORT)/

test-second-service: test-second-service-lb test-second-service-direct

delete-second-service:
	@echo "==> Deleting second-service..."
	sudo ./$(BINARY) delete service second-service

list:
	@echo "==> Listing active workloads..."
	sudo ./$(BINARY) list

logs:
	@echo "==> Tailing logs (Ctrl+C to stop)..."
	sudo tail -f /var/log/second-service.log /var/log/envoy-paas.log

clean:
	@echo "==> Cleaning up..."
	@if [ -f $(BINARY) ]; then rm -f $(BINARY); fi
	@if [ -f ./scripts/clean_interfaces.sh ]; then sudo ./scripts/clean_interfaces.sh; fi
