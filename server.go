package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

type configService struct {
	path     string
	mu       sync.RWMutex
	reloadMu sync.Mutex
	digest   [sha256.Size]byte
	configs  map[string][]byte
}

func newConfigService(path string) (*configService, error) {
	service := &configService{path: path}
	if _, err := service.reload(); err != nil {
		return nil, err
	}
	return service, nil
}

func (service *configService) reload() (bool, error) {
	service.reloadMu.Lock()
	defer service.reloadMu.Unlock()
	data, err := os.ReadFile(service.path)
	if err != nil {
		return false, fmt.Errorf("read configuration: %w", err)
	}
	digest := sha256.Sum256(data)
	if digest == service.digest && service.configs != nil {
		return false, nil
	}
	configs, err := compileConfig(data)
	if err != nil {
		return false, err
	}
	service.mu.Lock()
	service.configs = configs
	service.digest = digest
	service.mu.Unlock()
	return true, nil
}

func (service *configService) watch(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			changed, err := service.reload()
			if err != nil {
				slog.Error("Configuration update rejected; keeping the last valid configuration", "error", err)
			} else if changed {
				slog.Info("Configuration reloaded", "path", service.path)
			}
		}
	}
}

func (service *configService) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /config/{slave}", func(writer http.ResponseWriter, request *http.Request) {
		service.mu.RLock()
		body, exists := service.configs[request.PathValue("slave")]
		service.mu.RUnlock()
		writer.Header().Set("Cache-Control", "no-store")
		if !exists {
			http.Error(writer, "unknown slave", http.StatusNotFound)
			return
		}
		writer.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		writer.Header().Set("Content-Length", fmt.Sprint(len(body)))
		if request.Method != http.MethodHead {
			if _, err := writer.Write(body); err != nil {
				slog.Warn("Could not write configuration response", "error", err)
			}
		}
	})
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if request.Method != http.MethodHead {
			if _, err := writer.Write([]byte("ok\n")); err != nil {
				slog.Warn("Could not write health response", "error", err)
			}
		}
	})
	return mux
}
