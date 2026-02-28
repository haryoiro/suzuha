package dyntools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/haryoiro/suzuha/internal/tool"
)

var validName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Manifest describes a dynamic tool stored on disk.
type Manifest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	CreatedAt   time.Time       `json:"created_at"`
}

// Manager handles creation, compilation, loading, and deletion of dynamic tools.
type Manager struct {
	toolsDir string
	registry *tool.Registry
	logger   *slog.Logger
	mu       sync.Mutex
	loaded   map[string]bool
}

// NewManager creates a new Manager.
func NewManager(toolsDir string, registry *tool.Registry, logger *slog.Logger) *Manager {
	return &Manager{
		toolsDir: toolsDir,
		registry: registry,
		logger:   logger,
		loaded:   make(map[string]bool),
	}
}

// LoadAll scans toolsDir and registers all valid compiled tools.
func (m *Manager) LoadAll() error {
	if err := os.MkdirAll(m.toolsDir, 0755); err != nil {
		return fmt.Errorf("dyntools: create tools dir: %w", err)
	}

	entries, err := os.ReadDir(m.toolsDir)
	if err != nil {
		return fmt.Errorf("dyntools: read tools dir: %w", err)
	}

	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if err := m.loadOne(name); err != nil {
			m.logger.Warn("dyntools: skip tool", "name", name, "error", err)
			continue
		}
		count++
	}

	if count > 0 {
		m.logger.Info("dyntools: loaded tools", "count", count)
	}
	return nil
}

func (m *Manager) loadOne(name string) error {
	dir := filepath.Join(m.toolsDir, name)
	manifestPath := filepath.Join(dir, "manifest.json")
	binaryPath := filepath.Join(dir, "tool")

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}

	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}

	if _, err := os.Stat(binaryPath); err != nil {
		return fmt.Errorf("binary not found: %w", err)
	}

	st := NewScriptTool(manifest.Name, manifest.Description, manifest.InputSchema, binaryPath)
	m.registry.Register(st)
	m.loaded[manifest.Name] = true
	m.logger.Info("dyntools: registered", "tool", manifest.Name)
	return nil
}

// Create writes source, compiles, and registers a new dynamic tool.
func (m *Manager) Create(name, description string, inputSchema json.RawMessage, sourceCode string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !validName.MatchString(name) {
		return fmt.Errorf("invalid tool name %q: must match [a-z][a-z0-9_]{0,63}", name)
	}

	// Check for conflicts with non-dynamic tools.
	if _, exists := m.registry.Get(name); exists && !m.loaded[name] {
		return fmt.Errorf("tool %q already exists as a builtin tool", name)
	}

	dir := filepath.Join(m.toolsDir, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}

	// Write manifest.
	manifest := Manifest{
		Name:        name,
		Description: description,
		InputSchema: inputSchema,
		CreatedAt:   time.Now(),
	}
	manifestData, _ := json.MarshalIndent(manifest, "", "  ")
	manifestPath := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(manifestPath, manifestData, 0644); err != nil {
		os.RemoveAll(dir)
		return fmt.Errorf("write manifest: %w", err)
	}

	// Generate and write source.
	source := GenerateSource(sourceCode)
	mainPath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(mainPath, []byte(source), 0644); err != nil {
		os.RemoveAll(dir)
		return fmt.Errorf("write source: %w", err)
	}

	// Compile.
	binaryPath := filepath.Join(dir, "tool")
	cmd := exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-o", binaryPath, mainPath)
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		compileErr := fmt.Errorf("compilation failed:\n%s", string(output))
		os.RemoveAll(dir)
		return compileErr
	}

	// Register.
	st := NewScriptTool(name, description, inputSchema, binaryPath)
	m.registry.Register(st)
	m.loaded[name] = true
	m.logger.Info("dyntools: created", "tool", name)
	return nil
}

// Delete unregisters and removes a dynamic tool from disk.
func (m *Manager) Delete(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.loaded[name] {
		return fmt.Errorf("tool %q is not a dynamic tool", name)
	}

	m.registry.Unregister(name)
	delete(m.loaded, name)

	dir := filepath.Join(m.toolsDir, name)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove dir: %w", err)
	}

	m.logger.Info("dyntools: deleted", "tool", name)
	return nil
}

// List returns manifests of all dynamic tools.
func (m *Manager) List() ([]Manifest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var manifests []Manifest
	for name := range m.loaded {
		dir := filepath.Join(m.toolsDir, name)
		data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
		if err != nil {
			continue
		}
		var manifest Manifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			continue
		}
		manifests = append(manifests, manifest)
	}
	return manifests, nil
}

// IsDynamic returns true if the named tool is managed by this Manager.
func (m *Manager) IsDynamic(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loaded[name]
}

// GenerateCode uses Claude Code CLI to generate Go source code for a tool.
func (m *Manager) GenerateCode(ctx context.Context, name, description, inputSchema, prompt string) (string, error) {
	codegenPrompt := fmt.Sprintf(`Generate ONLY the Go source code for a tool function. No explanation, no markdown fences.

Tool name: %s
Description: %s
Input JSON Schema: %s
Requirements: %s

The code must define:
  func run(input json.RawMessage) (string, error)

Rules:
- Do NOT include "package main" or import statements (they are provided by a template).
- Available imports: encoding/json, fmt, io, math, net/http, net/url, os, regexp, sort, strconv, strings, time, bytes
- Parse input with json.Unmarshal into a struct.
- Return the result as a string and nil error on success.
- Return "" and an error on failure.
- You may define helper functions.
- Output ONLY valid Go code, nothing else.`, name, description, inputSchema, prompt)

	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude", "-p", codegenPrompt, "--output-format", "text")
	cmd.Env = os.Environ()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	m.logger.Info("dyntools: generating code with claude", "tool", name)
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("claude code generation timed out (120s)")
		}
		return "", fmt.Errorf("claude CLI failed: %v\nstderr: %s", err, stderr.String())
	}

	code := stdout.String()
	if code == "" {
		return "", fmt.Errorf("claude returned empty output")
	}

	m.logger.Info("dyntools: code generated", "tool", name, "length", len(code))
	return code, nil
}
