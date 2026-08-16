package cmd

import (
	"testing"
)

func TestRootCmd(t *testing.T) {
	rootCmd := GetRootCmd()
	if rootCmd == nil {
		t.Fatal("expected rootCmd to be non-nil")
	}

	if rootCmd.Use != "paas" {
		t.Errorf("expected rootCmd.Use to be 'paas', got '%s'", rootCmd.Use)
	}

	commands := make(map[string]bool)
	for _, c := range rootCmd.Commands() {
		commands[c.Name()] = true
	}

	expectedCmds := []string{"create", "delete", "cleanup", "list"}
	for _, expected := range expectedCmds {
		if !commands[expected] {
			t.Errorf("expected subcommand '%s' to be registered on rootCmd", expected)
		}
	}
}

func TestCommandArgsValidation(t *testing.T) {
	createCmd := newCreateCmd()
	if createCmd.Args == nil {
		t.Fatal("expected createCmd to have Args validator")
	}
	if err := createCmd.Args(createCmd, []string{}); err == nil {
		t.Error("expected error when running create without args")
	}
	if err := createCmd.Args(createCmd, []string{"config.yaml"}); err != nil {
		t.Errorf("expected no error when running create with 1 arg, got: %v", err)
	}

	deleteCmd := newDeleteCmd()
	if deleteCmd.Args == nil {
		t.Fatal("expected deleteCmd to have Args validator")
	}
	if err := deleteCmd.Args(deleteCmd, []string{}); err == nil {
		t.Error("expected error when running delete without args")
	}
	if err := deleteCmd.Args(deleteCmd, []string{"service"}); err == nil {
		t.Error("expected error when running delete with only 1 arg")
	}
	if err := deleteCmd.Args(deleteCmd, []string{"service", "test"}); err != nil {
		t.Errorf("expected no error when running delete with 2 args, got: %v", err)
	}
}

func TestNewRedisClient(t *testing.T) {
	rdb := newRedisClient()
	if rdb == nil {
		t.Fatal("expected redis client to be non-nil")
	}
	defer rdb.Close()
}
