package prompt

import (
	"testing"

	"github.com/haryoiro/suzuha/internal/port/embedder"
)

func TestParseDataURI(t *testing.T) {
	tests := []struct {
		name     string
		uri      string
		wantData bool
		wantMime string
	}{
		{
			"valid image PNG",
			"data:image/png;base64,aGVsbG8=",
			true,
			"image/png",
		},
		{
			"valid image JPEG",
			"data:image/jpeg;base64,d29ybGQ=",
			true,
			"image/jpeg",
		},
		{
			"valid audio WAV",
			"data:audio/wav;base64,AAAA",
			true,
			"audio/wav",
		},
		{
			"not a data URI",
			"https://example.com/image.png",
			false,
			"",
		},
		{
			"data URI without comma",
			"data:image/png;base64",
			false,
			"",
		},
		{
			"invalid base64",
			"data:image/png;base64,!!!invalid!!!",
			false,
			"",
		},
		{
			"empty string",
			"",
			false,
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, mime := parseDataURI(tt.uri)
			if tt.wantData && data == nil {
				t.Error("expected non-nil data, got nil")
			}
			if !tt.wantData && data != nil {
				t.Errorf("expected nil data, got %d bytes", len(data))
			}
			if mime != tt.wantMime {
				t.Errorf("mime = %q, want %q", mime, tt.wantMime)
			}
		})
	}
}

func TestModalityFromMime(t *testing.T) {
	tests := []struct {
		name string
		mime string
		want embedding.Modality
	}{
		{"image/png", "image/png", embedding.ModalityImage},
		{"image/jpeg", "image/jpeg", embedding.ModalityImage},
		{"image/webp", "image/webp", embedding.ModalityImage},
		{"audio/wav", "audio/wav", embedding.ModalityAudio},
		{"audio/mp3", "audio/mp3", embedding.ModalityAudio},
		{"text/plain", "text/plain", embedding.ModalityText},
		{"application/json", "application/json", embedding.ModalityText},
		{"empty string", "", embedding.ModalityText},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := modalityFromMime(tt.mime)
			if got != tt.want {
				t.Errorf("modalityFromMime(%q) = %q, want %q", tt.mime, got, tt.want)
			}
		})
	}
}
