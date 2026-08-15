package parser

import (
	"testing"
)

func TestParseDatabaseValid(t *testing.T) {
	yamlBody := `
name: testdb
technology: postgresql
username: postgres_user
password: secretpassword
`
	var d Database
	err := d.ParseYaml(yamlBody)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if d.Name != "testdb" {
		t.Errorf("expected name 'testdb', got '%s'", d.Name)
	}
	if d.Technology != PostgreSQL {
		t.Errorf("expected technology 'postgresql', got '%s'", d.Technology)
	}
	if d.Username != "postgres_user" {
		t.Errorf("expected username 'postgres_user', got '%s'", d.Username)
	}
	if d.Password != "secretpassword" {
		t.Errorf("expected password 'secretpassword', got '%s'", d.Password)
	}
	if d.InitScript != nil {
		t.Errorf("expected nil init script, got %v", d.InitScript)
	}
}

func TestParseDatabaseWithInitScript(t *testing.T) {
	yamlBody := `
name: mydb
technology: postgresql
username: user
password: pass
initscript: /path/to/init.sql
`
	var d Database
	err := d.ParseYaml(yamlBody)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if d.InitScript == nil || *d.InitScript != "/path/to/init.sql" {
		t.Errorf("expected initscript '/path/to/init.sql', got %v", d.InitScript)
	}
}

func TestParseDatabaseInvalidYaml(t *testing.T) {
	yamlBody := `
name: : invalid
`
	var d Database
	err := d.ParseYaml(yamlBody)
	if err == nil {
		t.Errorf("expected error for invalid YAML, got nil")
	}
}
