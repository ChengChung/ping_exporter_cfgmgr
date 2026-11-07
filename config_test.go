package main

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v2"
)

func TestCompileExample(t *testing.T) {
	data, err := os.ReadFile("example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compileConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name  string
		count int
	}{
		{"CAN-CT163", 2},
		{"CAN-CTCN2", 1},
	} {
		var cfg exporterConfig
		if err := yaml.UnmarshalStrict(compiled[testCase.name], &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg.Labels["location"] != "CN" || len(cfg.Targets) != testCase.count {
			t.Fatalf("unexpected config for %s: %#v", testCase.name, cfg)
		}
		if cfg.Targets[0]["host"] != "104.245.12.188" || cfg.Targets[0]["target"] != "xTom.SJC" {
			t.Fatalf("host labels not flattened: %#v", cfg.Targets[0])
		}
		if _, exists := cfg.Targets[0]["location"]; exists {
			t.Fatal("global labels should remain global")
		}
		if testCase.count == 2 && cfg.Targets[1]["target"] != "Zenlayer.HKG" {
			t.Fatal("host labels leaked between targets")
		}
	}
}

func TestCompileValidation(t *testing.T) {
	for _, testCase := range []struct {
		name string
		body string
		want string
	}{
		{"empty", "", "at least one slave"},
		{"unknown field", "slaves: [{name: node, typo: true}]", "field typo"},
		{"duplicate slave", "slaves: [{name: node}, {name: node}]", "duplicate slave"},
		{"unknown slave", "slaves: [{name: node}]\ntargets: [{group_name: group, slaves: [missing]}]", "unknown slave"},
		{"reserved label", "slaves: [{name: node, labels: {__name__: invalid}}]", "reserved label"},
		{"host label", "slaves: [{name: node}]\ntargets: [{group_name: group, hosts: [{host: example.com, labels: {host: other}}]}]", "reserved label"},
		{"duplicate key", "slaves: [{name: node, labels: {site: one, site: two}}]", "already set"},
		{"empty host", "slaves: [{name: node}]\ntargets: [{group_name: group, hosts: [{host: ''}]}]", "invalid host"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := compileConfig([]byte(testCase.body)); err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestCompileDuplicateHosts(t *testing.T) {
	body := "slaves: [{name: node}, {name: idle}]\ntargets:\n" +
		"  - {group_name: first, slaves: [node, node], hosts: [{host: example.com, labels: {site: one}}]}\n" +
		"  - {group_name: second, slaves: [node], hosts: [{host: example.com, labels: {site: one}}]}\n"
	compiled, err := compileConfig([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var cfg exporterConfig
	if err := yaml.UnmarshalStrict(compiled["node"], &cfg); err != nil || len(cfg.Targets) != 1 {
		t.Fatalf("duplicate hosts were not merged: %#v, %v", cfg, err)
	}
	if !strings.Contains(string(compiled["idle"]), "targets: []") {
		t.Fatal("idle slave should have an explicit empty target list")
	}
	conflict := strings.Replace(body, "second, slaves: [node], hosts: [{host: example.com, labels: {site: one}}]", "second, slaves: [node], hosts: [{host: example.com, labels: {site: two}}]", 1)
	if _, err := compileConfig([]byte(conflict)); err == nil || !strings.Contains(err.Error(), "conflicting labels") {
		t.Fatalf("expected conflicting host error, got %v", err)
	}
}
