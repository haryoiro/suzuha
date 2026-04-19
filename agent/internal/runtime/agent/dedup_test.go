package agent

import "testing"

func TestIsDuplicateResponse(t *testing.T) {
	ag := newTestAgent()

	tests := []struct {
		name    string
		channel string
		text    string
		want    bool
	}{
		{"first message", "ch1", "hello", false},
		{"same channel same text", "ch1", "hello", true},
		{"same channel different text", "ch1", "world", false},
		{"different channel same text", "ch2", "world", false},
		{"empty channel never duplicate", "", "hello", false},
		{"empty channel repeated", "", "hello", false},
		{"ch1 still tracks last", "ch1", "world", true},
		{"ch1 new text", "ch1", "foo", false},
		{"ch2 still tracks last", "ch2", "world", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ag.isDuplicateResponse(tt.channel, tt.text)
			if got != tt.want {
				t.Errorf("isDuplicateResponse(%q, %q) = %v, want %v", tt.channel, tt.text, got, tt.want)
			}
		})
	}
}
