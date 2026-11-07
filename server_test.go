package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeConfig(t *testing.T, path, content string) {
	t.Helper()
	temporary := path + ".new"
	if err := os.WriteFile(temporary, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, path); err != nil {
		t.Fatal(err)
	}
}

func requestConfig(handler http.Handler, method, path string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(method, path, nil))
	return response
}

func TestConfigHTTPAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	const initial = "slaves: [{name: node, labels: {location: CN}}]\ntargets: [{group_name: group, slaves: [node], hosts: [{host: example.com, labels: {target: site}}]}]"
	writeConfig(t, path, initial)
	service, err := newConfigService(path)
	if err != nil {
		t.Fatal(err)
	}
	handler := service.handler()
	response := requestConfig(handler, http.MethodGet, "/config/node")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "location: CN") || !strings.Contains(response.Body.String(), "target: site") {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body)
	}
	if response.Header().Get("Content-Type") != "application/yaml; charset=utf-8" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected headers: %v", response.Header())
	}
	for _, testCase := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodHead, "/config/node", http.StatusOK},
		{http.MethodGet, "/config/missing", http.StatusNotFound},
		{http.MethodPost, "/config/node", http.StatusMethodNotAllowed},
		{http.MethodGet, "/healthz", http.StatusOK},
	} {
		result := requestConfig(handler, testCase.method, testCase.path)
		if result.Code != testCase.status || (testCase.method == http.MethodHead && result.Body.Len() != 0) {
			t.Fatalf("unexpected %s %s response: %d %s", testCase.method, testCase.path, result.Code, result.Body)
		}
	}
	if changed, err := service.reload(); changed || err != nil {
		t.Fatalf("unchanged file reloaded: %v, %v", changed, err)
	}
	for _, invalid := range []string{"slaves: [", "", "slaves: [{name: node}]\ntargets: [{group_name: bad, slaves: [unknown]}]"} {
		writeConfig(t, path, invalid)
		if changed, err := service.reload(); changed || err == nil {
			t.Fatal("invalid update was accepted")
		}
		if got := requestConfig(handler, http.MethodGet, "/config/node").Body.String(); got != response.Body.String() {
			t.Fatalf("invalid update replaced last valid config: %s", got)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := service.reload(); err == nil {
		t.Fatal("expected missing file error")
	}
	writeConfig(t, path, "slaves: [{name: node, labels: {location: US}}, {name: other}]")
	if changed, err := service.reload(); !changed || err != nil {
		t.Fatalf("valid update failed: %v, %v", changed, err)
	}
	updated := requestConfig(handler, http.MethodGet, "/config/node")
	if !strings.Contains(updated.Body.String(), "location: US") || !strings.Contains(updated.Body.String(), "targets: []") {
		t.Fatalf("target removal or label change failed: %s", updated.Body)
	}
	writeConfig(t, path, "slaves: [{name: other}]")
	if _, err := service.reload(); err != nil {
		t.Fatal(err)
	}
	if got := requestConfig(handler, http.MethodGet, "/config/node").Code; got != http.StatusNotFound {
		t.Fatalf("removed slave status = %d", got)
	}
}

func TestConfigWatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, path, "slaves: [{name: before}]")
	service, err := newConfigService(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		service.watch(ctx, time.Millisecond)
	}()
	t.Cleanup(func() { cancel(); workers.Wait() })
	writeConfig(t, path, "slaves: [{name: after}]")
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	handler := service.handler()
	for {
		if requestConfig(handler, http.MethodGet, "/config/after").Code == http.StatusOK {
			break
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("watcher did not load the atomically replaced configuration")
		}
	}
	cancel()
	workers.Wait()
}

func TestInvalidInitialConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if _, err := newConfigService(path); err == nil {
		t.Fatal("missing initial file was accepted")
	}
	writeConfig(t, path, "slaves: [")
	if _, err := newConfigService(path); err == nil {
		t.Fatal("invalid initial config was accepted")
	}
}
