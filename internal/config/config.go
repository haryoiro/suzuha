package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level application configuration.
type Config struct {
	LLM          LLM          `yaml:"llm"`
	Embedding    Embedding    `yaml:"embedding"`
	Discord      Discord      `yaml:"discord"`
	ToolServers  []ToolServer `yaml:"tool_servers"`
	Triggers     []Trigger    `yaml:"triggers"`
	Memory       Memory       `yaml:"memory"`
	Agent        Agent        `yaml:"agent"`
	Consolidator Consolidator `yaml:"consolidator"`
	Observe      Observe      `yaml:"observe"`
	Admin        Admin        `yaml:"admin"`
}

// LLM configures the language model provider.
type LLM struct {
	Provider  string `yaml:"provider"` // "openai", "zhipu", etc.
	Model     string `yaml:"model"`
	APIKey    string `yaml:"api_key"`
	APIBase   string `yaml:"api_base"`   // Custom base URL for OpenAI-compatible providers.
	MaxTokens int    `yaml:"max_tokens"`
}

// Embedding configures the embedding provider (can differ from LLM provider).
type Embedding struct {
	Provider string `yaml:"provider"` // "openai", "zhipu", etc. Defaults to llm.provider.
	Model    string `yaml:"model"`    // e.g. "text-embedding-3-small"
	APIKey   string `yaml:"api_key"`
	APIBase  string `yaml:"api_base"`
	Dims     int    `yaml:"dims"` // Target dimensions. 0 = model default.
}

// Discord configures the Discord bot connection.
type Discord struct {
	Token string `yaml:"token"`
	BotID string `yaml:"bot_id"`
}

// ToolServer configures a remote tool server connection.
type ToolServer struct {
	Name      string            `yaml:"name"`
	Type      string            `yaml:"type"`      // "websocket" or "mcp"
	Transport string            `yaml:"transport"` // "stdio", "http" (for mcp type)
	URL       string            `yaml:"url"`
	Command   string            `yaml:"command"`
	Args      []string          `yaml:"args"`
	Env       map[string]string `yaml:"env"`
}

// Trigger configures a proactive action trigger.
type Trigger struct {
	Name     string         `yaml:"name"`
	Type     string         `yaml:"type"` // "cron" or "condition"
	Interval time.Duration  `yaml:"interval"`
	Payload  map[string]any `yaml:"payload"`
}

// Memory configures the long-term memory store.
type Memory struct {
	DBPath string `yaml:"db_path"`
}

// Agent configures agent behavior.
type Agent struct {
	PromptDir         string  `yaml:"prompt_dir"`
	SystemPrompt      string  `yaml:"-"` // assembled from PromptDir files
	InterestThreshold float64 `yaml:"interest_threshold"`
	ContextWindowPct  float64 `yaml:"context_window_pct"` // trigger compaction at this %
}

// Consolidator configures the consolidator process connection.
type Consolidator struct {
	Address     string    `yaml:"address"`      // gRPC address, e.g. "localhost:50051"
	AgentNotify string    `yaml:"agent_notify"` // Agent's notification gRPC address, e.g. "localhost:50052"
	Scheduler   Scheduler `yaml:"scheduler"`
}

// Scheduler configures the cron scheduler in the Consolidator process.
type Scheduler struct {
	Enabled    bool       `yaml:"enabled"`
	Timezone   string     `yaml:"timezone"`    // IANA timezone (e.g. "Asia/Tokyo"). Defaults to UTC.
	QuietHours QuietHours `yaml:"quiet_hours"` // Suppress notifications during these hours.
	Jobs       []CronJob  `yaml:"jobs"`
}

// QuietHours defines a time window during which notifications are suppressed.
// Start and End are in "HH:MM" format (24h) in the configured timezone.
type QuietHours struct {
	Enabled bool   `yaml:"enabled"`
	Start   string `yaml:"start"` // e.g. "23:00"
	End     string `yaml:"end"`   // e.g. "08:00"
}

// CronJob defines a scheduled job. Task must match a registered CronTask.Name().
type CronJob struct {
	Name   string         `yaml:"name"`   // human-readable job name
	Task   string         `yaml:"task"`   // CronTask name to execute
	Cron   string         `yaml:"cron"`   // cron expression or @every syntax
	Config map[string]any `yaml:"config"` // task-specific configuration
}

// Observe configures observability.
type Observe struct {
	LogLevel    string `yaml:"log_level"`    // "debug", "info", "warn", "error"
	MetricsAddr string `yaml:"metrics_addr"` // e.g. ":9090"
}

// Admin configures the admin dashboard service.
type Admin struct {
	Addr         string `yaml:"addr"`           // e.g. ":8080"
	AgentMetrics string `yaml:"agent_metrics"`  // e.g. "http://agent:9090/metrics"
	AgentLogs    string `yaml:"agent_logs"`     // e.g. "http://agent:9090/internal/logs"
	AgentContext string `yaml:"agent_context"`  // e.g. "http://agent:9090/internal/context"
	ConsolLogs   string `yaml:"consol_logs"`    // e.g. "http://consolidator:9090/internal/logs"
	StaticDir    string `yaml:"static_dir"`     // path to built SPA assets
}

// Load reads and parses a config file from the given path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}

	cfg.applyEnv()
	cfg.setDefaults()

	configDir := filepath.Dir(path)
	if err := cfg.loadPromptFiles(configDir); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// loadPromptFiles reads IDENTITY.md and SOUL.md from Agent.PromptDir
// and assembles them into Agent.SystemPrompt.
func (c *Config) loadPromptFiles(configDir string) error {
	dir := c.Agent.PromptDir
	if dir == "" {
		return nil
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(configDir, dir)
	}

	var parts []string
	for _, name := range []string{"IDENTITY.md", "SOUL.md"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("config: read %s: %w", name, err)
		}
		parts = append(parts, strings.TrimSpace(string(data)))
	}
	c.Agent.SystemPrompt = strings.Join(parts, "\n\n")
	return nil
}

func (c *Config) applyEnv() {
	if v := os.Getenv("LLM_API_KEY"); v != "" {
		c.LLM.APIKey = v
	}
	if v := os.Getenv("EMBEDDING_API_KEY"); v != "" {
		c.Embedding.APIKey = v
	}
	if v := os.Getenv("DISCORD_TOKEN"); v != "" {
		c.Discord.Token = v
	}
}

func (c *Config) setDefaults() {
	// Embedding defaults: inherit from LLM if not set.
	if c.Embedding.Provider == "" {
		c.Embedding.Provider = c.LLM.Provider
	}
	if c.Embedding.APIKey == "" {
		c.Embedding.APIKey = c.LLM.APIKey
	}
	if c.Embedding.APIBase == "" && c.Embedding.Provider == c.LLM.Provider {
		c.Embedding.APIBase = c.LLM.APIBase
	}
	if c.Embedding.Dims == 0 {
		c.Embedding.Dims = 1024
	}
	if c.Memory.DBPath == "" {
		c.Memory.DBPath = "memory.db"
	}
	if c.Agent.ContextWindowPct == 0 {
		c.Agent.ContextWindowPct = 0.8
	}
	if c.Agent.InterestThreshold == 0 {
		c.Agent.InterestThreshold = 0.5
	}
	if c.Consolidator.Address == "" {
		c.Consolidator.Address = "localhost:50051"
	}
	if c.Consolidator.AgentNotify == "" {
		c.Consolidator.AgentNotify = "localhost:50052"
	}
	if c.Observe.LogLevel == "" {
		c.Observe.LogLevel = "info"
	}
	if c.Observe.MetricsAddr == "" {
		c.Observe.MetricsAddr = ":9090"
	}
	if c.Admin.Addr == "" {
		c.Admin.Addr = ":8080"
	}
	if c.Admin.AgentMetrics == "" {
		c.Admin.AgentMetrics = "http://localhost:9090/metrics"
	}
	if c.Admin.AgentLogs == "" {
		c.Admin.AgentLogs = "http://localhost:9090/internal/logs"
	}
	if c.Admin.AgentContext == "" {
		c.Admin.AgentContext = "http://localhost:9090/internal/context"
	}
}
