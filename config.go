package main

import (
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v2"
)

type config struct {
	Slaves  []slaveConfig `yaml:"slaves"`
	Targets []targetGroup `yaml:"targets"`
}

type slaveConfig struct {
	Name   string            `yaml:"name"`
	Labels map[string]string `yaml:"labels"`
}

type targetGroup struct {
	Name   string       `yaml:"group_name"`
	Slaves []string     `yaml:"slaves"`
	Hosts  []hostConfig `yaml:"hosts"`
}

type hostConfig struct {
	Host   string            `yaml:"host"`
	Labels map[string]string `yaml:"labels"`
}

type exporterConfig struct {
	Labels  map[string]string   `yaml:"labels,omitempty"`
	Targets []map[string]string `yaml:"targets"`
}

var labelName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func validateLabels(labels map[string]string) error {
	for name, value := range labels {
		if !labelName.MatchString(name) || strings.HasPrefix(name, "__") || name == "host" || name == "type" {
			return fmt.Errorf("invalid or reserved label name %q", name)
		}
		if !utf8.ValidString(value) {
			return fmt.Errorf("label %q must contain valid UTF-8", name)
		}
	}
	return nil
}

func compileConfig(data []byte) (map[string][]byte, error) {
	var source config
	if err := yaml.UnmarshalStrict(data, &source); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if len(source.Slaves) == 0 {
		return nil, fmt.Errorf("config must define at least one slave")
	}
	configs := make(map[string]*exporterConfig, len(source.Slaves))
	seenHosts := make(map[string]map[string]map[string]string, len(source.Slaves))
	for _, slave := range source.Slaves {
		if slave.Name == "" || strings.TrimSpace(slave.Name) != slave.Name || strings.ContainsAny(slave.Name, "/\\") || slave.Name == "." || slave.Name == ".." {
			return nil, fmt.Errorf("invalid slave name %q", slave.Name)
		}
		if _, exists := configs[slave.Name]; exists {
			return nil, fmt.Errorf("duplicate slave %q", slave.Name)
		}
		if err := validateLabels(slave.Labels); err != nil {
			return nil, fmt.Errorf("slave %q: %w", slave.Name, err)
		}
		configs[slave.Name] = &exporterConfig{Labels: slave.Labels, Targets: make([]map[string]string, 0)}
		seenHosts[slave.Name] = make(map[string]map[string]string)
	}
	groups := make(map[string]bool)
	for _, group := range source.Targets {
		if strings.TrimSpace(group.Name) == "" || groups[group.Name] {
			return nil, fmt.Errorf("empty or duplicate group name %q", group.Name)
		}
		groups[group.Name] = true
		members := make(map[string]bool)
		for _, name := range group.Slaves {
			if _, exists := configs[name]; !exists {
				return nil, fmt.Errorf("group %q references unknown slave %q", group.Name, name)
			}
			members[name] = true
		}
		for _, host := range group.Hosts {
			if host.Host == "" || strings.ContainsAny(host.Host, " \t\r\n") {
				return nil, fmt.Errorf("group %q contains invalid host %q", group.Name, host.Host)
			}
			if err := validateLabels(host.Labels); err != nil {
				return nil, fmt.Errorf("host %q: %w", host.Host, err)
			}
			target := map[string]string{"host": host.Host}
			maps.Copy(target, host.Labels)
			for name := range members {
				if previous, exists := seenHosts[name][host.Host]; exists {
					if !reflect.DeepEqual(previous, target) {
						return nil, fmt.Errorf("slave %q has conflicting labels for host %q", name, host.Host)
					}
					continue
				}
				configs[name].Targets = append(configs[name].Targets, target)
				seenHosts[name][host.Host] = target
			}
		}
	}
	compiled := make(map[string][]byte, len(configs))
	for name, cfg := range configs {
		body, err := yaml.Marshal(cfg)
		if err != nil {
			return nil, fmt.Errorf("encode slave %q: %w", name, err)
		}
		compiled[name] = body
	}
	return compiled, nil
}
