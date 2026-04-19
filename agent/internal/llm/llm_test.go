package llm

import (
	"log/slog"
	"testing"
)

func buildTestClient(t *testing.T, roles map[string]roleProvider) *Client {
	t.Helper()
	return &Client{roles: roles, logger: slog.Default()}
}

func TestWithCapability(t *testing.T) {
	tests := []struct {
		name       string
		roles      map[string]roleProvider
		role       string
		capability string
		wantNil    bool
		wantInline bool
		wantModel  string
	}{
		{
			"inline",
			map[string]roleProvider{
				"conversation": {capabilities: []string{"text", "vision"}, model: "gpt-4o"},
			},
			"conversation", "vision",
			false, true, "gpt-4o",
		},
		{
			"fallback",
			map[string]roleProvider{
				"conversation": {capabilities: []string{"text"}, model: "glm-4.7"},
				"vision":       {capabilities: []string{"text", "vision"}, model: "gpt-4.1-mini"},
			},
			"conversation", "vision",
			false, false, "gpt-4.1-mini",
		},
		{
			"not found",
			map[string]roleProvider{
				"conversation": {capabilities: []string{"text"}},
			},
			"conversation", "audio",
			true, false, "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := buildTestClient(t, tt.roles)
			rc, inline := c.WithCapability(tt.role, tt.capability)
			if tt.wantNil {
				if rc != nil {
					t.Error("expected RoleClient = nil")
				}
				return
			}
			if rc == nil {
				t.Fatal("RoleClient が nil")
			}
			if inline != tt.wantInline {
				t.Errorf("inline = %v, want %v", inline, tt.wantInline)
			}
			if rc.resolve().model != tt.wantModel {
				t.Errorf("model = %q, want %q", rc.resolve().model, tt.wantModel)
			}
		})
	}
}

func TestFor(t *testing.T) {
	tests := []struct {
		name      string
		roles     map[string]roleProvider
		role      string
		wantModel string
	}{
		{
			"direct role",
			map[string]roleProvider{
				"conversation": {model: "conv-model"},
				"background":   {model: "bg-model"},
			},
			"background", "bg-model",
		},
		{
			"fallback to background",
			map[string]roleProvider{
				"conversation": {model: "conv-model"},
				"background":   {model: "bg-model"},
			},
			"diary", "bg-model",
		},
		{
			"fallback to conversation",
			map[string]roleProvider{
				"conversation": {model: "conv-model"},
			},
			"background", "conv-model",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := buildTestClient(t, tt.roles)
			rc := c.For(tt.role)
			if rc.resolve().model != tt.wantModel {
				t.Errorf("model = %q, want %q", rc.resolve().model, tt.wantModel)
			}
		})
	}
}

func TestSwapRoleSpec(t *testing.T) {
	t.Run("update existing role", func(t *testing.T) {
		c := buildTestClient(t, map[string]roleProvider{
			"conversation": {model: "old-model"},
		})
		c.SwapRoleSpec("conversation", RoleSpec{
			ProviderName: "openai",
			ModelID:      "new-model",
			APIBase:      "https://api.openai.com/v1",
			MaxContext:   100000,
			Capabilities: []string{"text", "vision"},
		})
		rp := c.roles["conversation"]
		if rp.model != "new-model" {
			t.Errorf("model = %q, want new-model", rp.model)
		}
		if !rp.hasCapability("vision") {
			t.Error("vision capability が設定されるべき")
		}
	})

	t.Run("new role", func(t *testing.T) {
		c := buildTestClient(t, map[string]roleProvider{
			"conversation": {model: "conv"},
		})
		c.SwapRoleSpec("vision", RoleSpec{
			ProviderName: "openai",
			ModelID:      "vision-model",
			APIBase:      "https://api.openai.com/v1",
			Capabilities: []string{"text", "vision"},
		})
		if _, ok := c.roles["vision"]; !ok {
			t.Error("vision ロールが追加されるべき")
		}
	})

	t.Run("RoleClient reflects swap", func(t *testing.T) {
		c := buildTestClient(t, map[string]roleProvider{
			"background": {model: "old-bg"},
		})

		rc := c.For("background")
		if rc.resolve().model != "old-bg" {
			t.Fatalf("初期状態: got %q, want old-bg", rc.resolve().model)
		}

		c.SwapRoleSpec("background", RoleSpec{
			ProviderName: "openai",
			ModelID:      "new-bg",
			APIBase:      "https://api.openai.com/v1",
			Capabilities: []string{"text"},
		})

		if rc.resolve().model != "new-bg" {
			t.Errorf("SwapRoleSpec 後: got %q, want new-bg", rc.resolve().model)
		}
	})
}

func TestRoleProvider_HasCapability(t *testing.T) {
	rp := roleProvider{capabilities: []string{"text", "vision"}}
	tests := []struct {
		name string
		cap  string
		want bool
	}{
		{"text", "text", true},
		{"vision", "vision", true},
		{"audio not present", "audio", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rp.hasCapability(tt.cap); got != tt.want {
				t.Errorf("hasCapability(%q) = %v, want %v", tt.cap, got, tt.want)
			}
		})
	}
}
