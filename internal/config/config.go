package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level application configuration.
type Config struct {
	LLM         LLM          `yaml:"llm"`
	Discord     Discord      `yaml:"discord"`
	ToolServers []ToolServer `yaml:"tool_servers"`
	Triggers    []Trigger    `yaml:"triggers"`
	Memory      Memory       `yaml:"memory"`
	Agent       Agent        `yaml:"agent"`
	Consolidator Consolidator `yaml:"consolidator"`
	Observe     Observe      `yaml:"observe"`
}

// LLM configures the language model provider.
type LLM struct {
	Provider  string `yaml:"provider"`   // "openai", "anthropic", etc.
	Model     string `yaml:"model"`
	APIKey    string `yaml:"api_key"`
	MaxTokens int    `yaml:"max_tokens"`
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
	SystemPrompt      string  `yaml:"system_prompt"`
	InterestThreshold float64 `yaml:"interest_threshold"`
	ContextWindowPct  float64 `yaml:"context_window_pct"` // trigger compaction at this %
}

// Consolidator configures the consolidator process connection.
type Consolidator struct {
	Address string `yaml:"address"` // gRPC address, e.g. "localhost:50051"
}

// Observe configures observability.
type Observe struct {
	LogLevel    string `yaml:"log_level"`    // "debug", "info", "warn", "error"
	MetricsAddr string `yaml:"metrics_addr"` // e.g. ":9090"
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

	cfg.setDefaults()
	cfg.applyEnv()
	return &cfg, nil
}

func (c *Config) applyEnv() {
	if v := os.Getenv("LLM_API_KEY"); v != "" {
		c.LLM.APIKey = v
	}
	if v := os.Getenv("DISCORD_TOKEN"); v != "" {
		c.Discord.Token = v
	}
}

func (c *Config) setDefaults() {
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
	if c.Observe.LogLevel == "" {
		c.Observe.LogLevel = "info"
	}
	if c.Observe.MetricsAddr == "" {
		c.Observe.MetricsAddr = ":9090"
	}
}
