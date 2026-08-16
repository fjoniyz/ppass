package network

import (
	"strings"
	"testing"
)

func TestGetIpam(t *testing.T) {
	ipam, err := GetIpam()
	if err != nil {
		t.Fatalf("expected GetIpam to succeed, got %v", err)
	}
	if ipam == nil {
		t.Fatalf("expected non-nil ipam instance")
	}
}

func TestGetOrAcquireEnvoyIp(t *testing.T) {
	ip, err := GetOrAcquireEnvoyIp()
	if err != nil {
		t.Fatalf("failed to acquire Envoy IP: %v", err)
	}
	if ip != "10.0.0.1" {
		t.Errorf("expected Envoy IP to be '10.0.0.1', got '%s'", ip)
	}
}

func TestGetAndReleaseIp(t *testing.T) {
	ipamStruct := GetIp()
	if ipamStruct.Ip == "" {
		t.Fatalf("expected acquired IP, got empty string")
	}
	if !strings.HasPrefix(ipamStruct.Ip, "10.0.0.") {
		t.Errorf("expected IP in 10.0.0.x subnet, got '%s'", ipamStruct.Ip)
	}

	// Release the IP back
	err := ReleaseIp(ipamStruct.Ip)
	if err != nil {
		t.Errorf("failed to release IP %s: %v", ipamStruct.Ip, err)
	}
}
