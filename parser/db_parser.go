package parser

import (
	"log/slog"

	"gopkg.in/yaml.v2"
)

type DBTechnology string

// Supported database technologies
const (
	PostgreSQL DBTechnology = "postgresql"
	MySQL      DBTechnology = "mysql"
	SQLite     DBTechnology = "sqlite"
)

type Database struct {
	Name       string
	Technology DBTechnology
	Username   string
	Password   string
	InitScript *string // Optional init script for the db
	Pid        string
}

func (d *Database) ParseYaml(body string) error {
	if err := yaml.Unmarshal([]byte(body), d); err != nil {
		slog.Error("Failed to unmarshal database config", "error", err)
		return err
	}

	return nil
}
