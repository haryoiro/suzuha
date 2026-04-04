package llm

import (
	"log/slog"
	"testing"
)

func buildTestClient(t *testing.T, roles map[string]roleProvider) *Client {
	t.Helper()
	return &Client{roles: roles, logger: slog.Default()}
}

func TestHasVision_NativeCapability(t *testing.T) {
	c := buildTestClient(t, map[string]roleProvider{
		"conversation": {capabilities: []string{"text", "vision"}},
	})
	if !c.HasVision() {
		t.Error("conversation が vision 対応なら HasVision() = true")
	}
	if !c.IsVisionCapable() {
		t.Error("conversation が vision 対応なら IsVisionCapable() = true")
	}
}

func TestHasVision_SeparateVisionRole(t *testing.T) {
	c := buildTestClient(t, map[string]roleProvider{
		"conversation": {capabilities: []string{"text"}},
		"vision":       {capabilities: []string{"text", "vision"}},
	})
	if !c.HasVision() {
		t.Error("vision ロールが存在するなら HasVision() = true")
	}
	if c.IsVisionCapable() {
		t.Error("conversation は vision 非対応なので IsVisionCapable() = false")
	}
}

func TestHasVision_NoVision(t *testing.T) {
	c := buildTestClient(t, map[string]roleProvider{
		"conversation": {capabilities: []string{"text"}},
	})
	if c.HasVision() {
		t.Error("vision ロールがなく conversation も非対応なら HasVision() = false")
	}
}

func TestWithCapability_Inline(t *testing.T) {
	c := buildTestClient(t, map[string]roleProvider{
		"conversation": {capabilities: []string{"text", "vision"}, model: "gpt-4o"},
	})
	rc, inline := c.WithCapability("conversation", "vision")
	if rc == nil {
		t.Fatal("RoleClient が nil")
	}
	if !inline {
		t.Error("conversation がネイティブ対応なら inline = true")
	}
	if rc.resolve().model != "gpt-4o" {
		t.Errorf("conversation のモデルを期待、got %q", rc.resolve().model)
	}
}

func TestWithCapability_Fallback(t *testing.T) {
	c := buildTestClient(t, map[string]roleProvider{
		"conversation": {capabilities: []string{"text"}, model: "glm-4.7"},
		"vision":       {capabilities: []string{"text", "vision"}, model: "gpt-4.1-mini"},
	})
	rc, inline := c.WithCapability("conversation", "vision")
	if rc == nil {
		t.Fatal("RoleClient が nil")
	}
	if inline {
		t.Error("conversation が非対応でフォールバック時は inline = false")
	}
	if rc.resolve().model != "gpt-4.1-mini" {
		t.Errorf("vision ロールのモデルを期待、got %q", rc.resolve().model)
	}
}

func TestWithCapability_NotFound(t *testing.T) {
	c := buildTestClient(t, map[string]roleProvider{
		"conversation": {capabilities: []string{"text"}},
	})
	rc, _ := c.WithCapability("conversation", "audio")
	if rc != nil {
		t.Error("audio 対応がなければ RoleClient = nil")
	}
}

func TestFor_DirectRole(t *testing.T) {
	c := buildTestClient(t, map[string]roleProvider{
		"conversation": {model: "conv-model"},
		"background":   {model: "bg-model"},
	})
	rc := c.For("background")
	if rc.resolve().model != "bg-model" {
		t.Errorf("background ロールを期待、got %q", rc.resolve().model)
	}
}

func TestFor_FallbackToBackground(t *testing.T) {
	c := buildTestClient(t, map[string]roleProvider{
		"conversation": {model: "conv-model"},
		"background":   {model: "bg-model"},
	})
	rc := c.For("diary")
	if rc.resolve().model != "bg-model" {
		t.Errorf("diary は未割当 → background にフォールバック、got %q", rc.resolve().model)
	}
}

func TestFor_FallbackToConversation(t *testing.T) {
	c := buildTestClient(t, map[string]roleProvider{
		"conversation": {model: "conv-model"},
	})
	rc := c.For("background")
	// background が存在しない → conversation にフォールバック
	if rc.resolve().model != "conv-model" {
		t.Errorf("background なし → conversation にフォールバック、got %q", rc.resolve().model)
	}
}

func TestSwapRoleSpec(t *testing.T) {
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
}

func TestSwapRoleSpec_NewRole(t *testing.T) {
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
}

func TestRoleClient_ReflectsSwapRoleSpec(t *testing.T) {
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
}

func TestRoleProvider_HasCapability(t *testing.T) {
	rp := roleProvider{capabilities: []string{"text", "vision"}}
	if !rp.hasCapability("text") {
		t.Error("text")
	}
	if !rp.hasCapability("vision") {
		t.Error("vision")
	}
	if rp.hasCapability("audio") {
		t.Error("audio はないはず")
	}
}
