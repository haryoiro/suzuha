package affinity

import "testing"

func TestParseDeltas_NoChange(t *testing.T) {
	deltas := parseDeltas("変化なし")
	if len(deltas) != 0 {
		t.Errorf("expected 0 deltas for '変化なし', got %d", len(deltas))
	}
}

func TestParseDeltas_SingleDelta(t *testing.T) {
	input := `- [delta] user_id=123 platform=discord axis=closeness delta=+0.3 reason=(楽) 楽しく話した`
	deltas := parseDeltas(input)

	if len(deltas) != 1 {
		t.Fatalf("expected 1 delta, got %d", len(deltas))
	}
	d := deltas[0]
	if d.platformUserID != "123" {
		t.Errorf("user_id: got %s", d.platformUserID)
	}
	if d.platform != "discord" {
		t.Errorf("platform: got %s", d.platform)
	}
	if d.axis != "closeness" {
		t.Errorf("axis: got %s", d.axis)
	}
	if d.delta != 0.3 {
		t.Errorf("delta: got %f", d.delta)
	}
	if d.reason != "(楽) 楽しく話した" {
		t.Errorf("reason: got %q", d.reason)
	}
}

func TestParseDeltas_MultipleAxes(t *testing.T) {
	input := `- [delta] user_id=123 platform=discord axis=closeness delta=+0.2 reason=(楽) 雑談
- [delta] user_id=123 platform=discord axis=interest delta=+0.5 reason=(興) 面白い話題`
	deltas := parseDeltas(input)

	if len(deltas) != 2 {
		t.Fatalf("expected 2 deltas, got %d", len(deltas))
	}
	if deltas[0].axis != "closeness" {
		t.Errorf("delta[0] axis: got %s", deltas[0].axis)
	}
	if deltas[1].axis != "interest" {
		t.Errorf("delta[1] axis: got %s", deltas[1].axis)
	}
}

func TestParseDeltas_DefaultAxis(t *testing.T) {
	input := `- [delta] user_id=abc platform=discord delta=+0.1 reason=test`
	deltas := parseDeltas(input)

	if len(deltas) != 1 {
		t.Fatalf("expected 1 delta, got %d", len(deltas))
	}
	if deltas[0].axis != "closeness" {
		t.Errorf("axis: expected closeness (default), got %s", deltas[0].axis)
	}
}

func TestParseDeltas_ZeroDeltaIgnored(t *testing.T) {
	input := `- [delta] user_id=abc platform=discord axis=trust delta=0 reason=nothing`
	deltas := parseDeltas(input)

	if len(deltas) != 0 {
		t.Errorf("expected 0 deltas for zero delta, got %d", len(deltas))
	}
}

func TestParseDeltas_InvalidLine(t *testing.T) {
	input := `Some random text
not a delta line
- [delta] user_id=valid platform=discord axis=trust delta=+0.5 reason=ok`
	deltas := parseDeltas(input)

	if len(deltas) != 1 {
		t.Fatalf("expected 1 delta, got %d", len(deltas))
	}
	if deltas[0].platformUserID != "valid" {
		t.Errorf("user_id: got %s", deltas[0].platformUserID)
	}
}
