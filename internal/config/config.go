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
	Timezone     string       `yaml:"timezone"` // IANA timezone (e.g. "Asia/Tokyo"). Defaults to UTC.
	LLM          LLM          `yaml:"llm"`
	Embedding    Embedding    `yaml:"embedding"`
	Vision       Vision       `yaml:"vision"`
	Discord      Discord      `yaml:"discord"`
	Voice        Voice        `yaml:"voice"`
	ToolServers  []ToolServer `yaml:"tool_servers"`
	Triggers     []Trigger    `yaml:"triggers"`
	Memory       Memory       `yaml:"memory"`
	Agent        Agent        `yaml:"agent"`
	Consolidator Consolidator `yaml:"consolidator"`
	Observe      Observe      `yaml:"observe"`
	Admin        Admin        `yaml:"admin"`
	Location      Location     `yaml:"location"`
	Langfuse      Langfuse     `yaml:"langfuse"`
	EncryptionKey string       `yaml:"-"` // SUZUHA_ENCRYPTION_KEY 環境変数から設定 (hex 64文字 = 32byte)
}

// Langfuse configures LLM observability tracing via OTLP.
type Langfuse struct {
	Enabled   bool   `yaml:"enabled"`
	Endpoint  string `yaml:"endpoint"`   // e.g. "http://langfuse:3000"
	PublicKey string `yaml:"public_key"` // Langfuse project public key
	SecretKey string `yaml:"secret_key"` // Langfuse project secret key
}


// Location configures the Overland GPS tracking integration.
type Location struct {
	Enabled bool   `yaml:"enabled"`
	Token   string `yaml:"token"`
}

// LLM configures the language model provider.
type LLM struct {
	Provider  string        `yaml:"provider"` // "openai", "zhipu", etc. (legacy, used as fallback)
	Model     string        `yaml:"model"`    // (legacy)
	APIKey    string        `yaml:"api_key"`  // (legacy)
	APIBase   string        `yaml:"api_base"` // (legacy)
	MaxTokens int           `yaml:"max_tokens"`
	Providers []LLMProvider `yaml:"providers"` // Named provider connections.
	Presets   []LLMPreset   `yaml:"presets"`   // Deprecated: use providers instead.
}

// LLMProvider is a named connection to an LLM API provider.
// Defines only the connection info (type, credentials, endpoint).
// Model selection is done separately via role assignments.
type LLMProvider struct {
	Name    string `yaml:"name"`
	Type    string `yaml:"type"`     // "openai", "zhipu", "gemini", "qwen"
	APIKey  string `yaml:"api_key"`
	APIBase string `yaml:"api_base"`
}

// LLMPreset is a named LLM provider configuration that can be activated at runtime.
// Deprecated: use LLMProvider + DB role assignments instead.
type LLMPreset struct {
	Name      string `yaml:"name"`
	Provider  string `yaml:"provider"`
	Model     string `yaml:"model"`
	APIKey    string `yaml:"api_key"`
	APIBase   string `yaml:"api_base"`
	MaxTokens int    `yaml:"max_tokens"`
	Vision    bool   `yaml:"vision"`
}

// FindProvider returns the provider config with the given name, or nil if not found.
func (l *LLM) FindProvider(name string) *LLMProvider {
	for i := range l.Providers {
		if l.Providers[i].Name == name {
			return &l.Providers[i]
		}
	}
	return nil
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

// Voice configures voice chat integration.
type Voice struct {
	Enabled         bool          `yaml:"enabled"`
	STT             []STTProvider `yaml:"stt"`              // STT providers in priority order (first = highest)
	TTS             []TTSProvider `yaml:"tts"`              // TTS providers in priority order (first = highest)
	AllowedChannels []string      `yaml:"allowed_channels"` // VC channel IDs where voice is allowed (empty = all)
}

// STTProvider configures a single STT provider.
type STTProvider struct {
	Provider string `yaml:"provider"` // "deepgram", "whispercpp"
	APIKey   string `yaml:"api_key"`  // API key (deepgram)
	Model    string `yaml:"model"`    // Model name (deepgram: "nova-3")
	URL      string `yaml:"url"`      // Server URL (whispercpp: "http://whisper:8001")
}

// TTSProvider configures a single TTS provider.
type TTSProvider struct {
	Provider  string `yaml:"provider"`   // "voicevox", "sbv2"
	URL       string `yaml:"url"`        // Server URL
	SpeakerID int    `yaml:"speaker_id"` // VOICEVOX speaker ID
	Model     string `yaml:"model"`      // SBV2 model name
	Style     string `yaml:"style"`      // SBV2 style name
}

// Vision configures the vision language model for image understanding.
type Vision struct {
	Provider string `yaml:"provider"` // Defaults to llm.provider.
	Model    string `yaml:"model"`    // e.g. "glm-4.6v-flash"
	APIKey   string `yaml:"api_key"`
	APIBase  string `yaml:"api_base"`
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
	APIAddr     string    `yaml:"api_addr"`     // HTTP API address for manual triggers, e.g. ":9091"
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
	InternalAddr string `yaml:"internal_addr"` // e.g. ":9090"
}

// AdminAuth configures Basic authentication for the admin dashboard.
type AdminAuth struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// Admin configures the admin dashboard service.
type Admin struct {
	Addr            string    `yaml:"addr"`             // e.g. ":8080"
	AgentMetrics    string    `yaml:"agent_metrics"`    // e.g. "http://agent:9090/metrics"
	AgentLogs       string    `yaml:"agent_logs"`       // e.g. "http://agent:9090/internal/logs"
	AgentContext    string    `yaml:"agent_context"`    // e.g. "http://agent:9090/internal/context"
	ConsolLogs      string    `yaml:"consol_logs"`      // e.g. "http://consolidator:9090/internal/logs"
	ConsolidatorAPI string    `yaml:"consolidator_api"` // e.g. "http://consolidator:9091"
	StaticDir       string    `yaml:"static_dir"`       // path to built SPA assets
	PromptDir       string    `yaml:"prompt_dir"`       // path to prompt files (IDENTITY.md, SOUL.md)
	Auth            AdminAuth `yaml:"auth"`             // Basic authentication (optional)
}

// Load reads and parses a config file from the given path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: %s の読み込みに失敗: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: %s のパースに失敗: %w", path, err)
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
			return fmt.Errorf("config: %s の読み込みに失敗: %w", name, err)
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
	if v := os.Getenv("VISION_API_KEY"); v != "" {
		c.Vision.APIKey = v
	}
	if v := os.Getenv("DISCORD_TOKEN"); v != "" {
		c.Discord.Token = v
	}
	if v := os.Getenv("OVERLAND_TOKEN"); v != "" {
		c.Location.Token = v
	}
	if v := os.Getenv("LANGFUSE_PUBLIC_KEY"); v != "" {
		c.Langfuse.PublicKey = v
	}
	if v := os.Getenv("LANGFUSE_SECRET_KEY"); v != "" {
		c.Langfuse.SecretKey = v
	}
	if v := os.Getenv("SUZUHA_ENCRYPTION_KEY"); v != "" {
		c.EncryptionKey = v
	}
	if v := os.Getenv("DEEPGRAM_API_KEY"); v != "" {
		for i := range c.Voice.STT {
			if c.Voice.STT[i].Provider == "deepgram" {
				c.Voice.STT[i].APIKey = v
			}
		}
	}
}

func (c *Config) setDefaults() {
	// Timezone: fall back to consolidator.scheduler.timezone for backward compat.
	if c.Timezone == "" && c.Consolidator.Scheduler.Timezone != "" {
		c.Timezone = c.Consolidator.Scheduler.Timezone
	}

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
		c.Embedding.Dims = 1536
	}
	// Vision defaults: inherit from embedding if same provider, otherwise from LLM.
	if c.Vision.Provider == "" {
		c.Vision.Provider = c.LLM.Provider
	}
	if c.Vision.APIKey == "" {
		if c.Vision.Provider == c.Embedding.Provider {
			c.Vision.APIKey = c.Embedding.APIKey
		} else {
			c.Vision.APIKey = c.LLM.APIKey
		}
	}
	if c.Vision.APIBase == "" {
		switch c.Vision.Provider {
		case c.Embedding.Provider:
			c.Vision.APIBase = c.Embedding.APIBase
		case c.LLM.Provider:
			c.Vision.APIBase = c.LLM.APIBase
		}
	}
	// Provider API key defaults: inherit from LLM top-level key.
	for i := range c.LLM.Providers {
		if c.LLM.Providers[i].APIKey == "" {
			c.LLM.Providers[i].APIKey = c.LLM.APIKey
		}
		if c.LLM.Providers[i].Type == "" {
			c.LLM.Providers[i].Type = c.LLM.Providers[i].Name
		}
	}
	// Legacy preset API key defaults.
	for i := range c.LLM.Presets {
		if c.LLM.Presets[i].APIKey == "" {
			switch c.LLM.Presets[i].Provider {
			case c.Embedding.Provider:
				c.LLM.Presets[i].APIKey = c.Embedding.APIKey
			case c.Vision.Provider:
				c.LLM.Presets[i].APIKey = c.Vision.APIKey
			default:
				c.LLM.Presets[i].APIKey = c.LLM.APIKey
			}
		}
	}
	// Voice defaults: set default speaker_id for voicevox providers.
	for i := range c.Voice.TTS {
		if c.Voice.TTS[i].Provider == "voicevox" && c.Voice.TTS[i].SpeakerID == 0 {
			c.Voice.TTS[i].SpeakerID = 3 // zundamon normal
		}
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
	// Consolidator.Address and AgentNotify are no longer used (consolidation is in-process).
	// Fields kept for YAML backward compatibility.
	if c.Consolidator.APIAddr == "" {
		c.Consolidator.APIAddr = ":9091"
	}
	if c.Observe.LogLevel == "" {
		c.Observe.LogLevel = "info"
	}
	if c.Observe.InternalAddr == "" {
		c.Observe.InternalAddr = ":9090"
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
	if c.Admin.ConsolidatorAPI == "" {
		c.Admin.ConsolidatorAPI = "http://localhost:9091"
	}
	if c.Admin.PromptDir == "" {
		c.Admin.PromptDir = c.Agent.PromptDir
	}
}
