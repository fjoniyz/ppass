package parser

import (
	"fmt"
	"log/slog"
	"strings"

	"gopkg.in/yaml.v2"

	"private_paas/cmd"
)

type Technology string

const (
	Go     Technology = "go"
	Java   Technology = "java"
	Python Technology = "python"
)

type ServiceEnvoyConfig struct {
	UpstreamName string   `yaml:"upstreamname"`
	Servers      []string `yaml:"servers"`
	ListenPort   int      `yaml:"listenport"`
	Domain       string   `yaml:"domain"`
}

type Limitations struct {
	Memory string `yaml:"memory,omitempty"`
	CPU    string `yaml:"cpu,omitempty"`
	Pids   string `yaml:"pids,omitempty"`
}

type Service struct {
	Name        string             `yaml:"name"`
	Lb          bool               `yaml:"lb"`
	Path        string             `yaml:"path"`
	Technology  Technology         `yaml:"technology"`
	LbConfig    ServiceEnvoyConfig `yaml:"lbconfig"`
	Limitations Limitations        `yaml:"limitations"`
	Pid         int
	Process     cmd.ProcessStruct
}

func (s *Service) ParseService(body string) {
	if err := yaml.Unmarshal([]byte(body), s); err != nil {
		slog.Error("Failed to unmarshal service body", "error", err)
		return
	}
	s.Technology = Technology(strings.TrimSpace(string(s.Technology)))
	s.Name = strings.TrimSpace(s.Name)
	s.Path = strings.TrimSpace(s.Path)
	fmt.Println("File read successfully:", s.Name)
}
