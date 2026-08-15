package parser

import (
	"testing"
)

func TestParseServiceValid(t *testing.T) {
	yamlBody := `
name: test-service
lb: true
path: /path/to/test.py
technology: python
lbconfig:
  upstreamname: test_service
  listenport: 8080
  domain: test.local
  servers:
    - "10.0.0.2:8888"
limitations:
  memory: "100M"
  cpu: "50000 100000"
  pids: "20"
`
	var s Service
	s.ParseService(yamlBody)

	if s.Name != "test-service" {
		t.Errorf("expected name 'test-service', got '%s'", s.Name)
	}
	if !s.Lb {
		t.Errorf("expected Lb to be true")
	}
	if s.Technology != Python {
		t.Errorf("expected Technology to be 'python', got '%s'", s.Technology)
	}
	if s.LbConfig.UpstreamName != "test_service" {
		t.Errorf("expected upstream name 'test_service', got '%s'", s.LbConfig.UpstreamName)
	}
	if s.LbConfig.ListenPort != 8080 {
		t.Errorf("expected listen port 8080, got %d", s.LbConfig.ListenPort)
	}
	if s.LbConfig.Domain != "test.local" {
		t.Errorf("expected domain 'test.local', got '%s'", s.LbConfig.Domain)
	}
	if len(s.LbConfig.Servers) != 1 || s.LbConfig.Servers[0] != "10.0.0.2:8888" {
		t.Errorf("expected server '10.0.0.2:8888', got %v", s.LbConfig.Servers)
	}
	if s.Limitations.Memory != "100M" {
		t.Errorf("expected memory limit '100M', got '%s'", s.Limitations.Memory)
	}
	if s.Limitations.CPU != "50000 100000" {
		t.Errorf("expected cpu limit '50000 100000', got '%s'", s.Limitations.CPU)
	}
	if s.Limitations.Pids != "20" {
		t.Errorf("expected pids limit '20', got '%s'", s.Limitations.Pids)
	}
}

func TestParseServiceEmptyServers(t *testing.T) {
	yamlBody := `
name: second-service
lb: true
path: /path/to/second.py
technology: python
lbconfig:
  upstreamname: second_service
  listenport: 8081
  domain: second.local
`
	var s Service
	s.ParseService(yamlBody)

	if s.Name != "second-service" {
		t.Errorf("expected name 'second-service', got '%s'", s.Name)
	}
	if len(s.LbConfig.Servers) != 0 {
		t.Errorf("expected 0 servers initially, got %d", len(s.LbConfig.Servers))
	}
}

func TestParseServiceInvalidYaml(t *testing.T) {
	yamlBody := `
name: [invalid yaml
`
	var s Service
	// Should not panic, logs error and leaves struct empty
	s.ParseService(yamlBody)
	if s.Name != "" {
		t.Errorf("expected empty name on invalid YAML, got '%s'", s.Name)
	}
}
