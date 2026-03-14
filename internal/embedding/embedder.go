package embedding

import "context"

// Modality represents the type of data that can be embedded.
type Modality string

const (
	ModalityText  Modality = "text"
	ModalityImage Modality = "image"
	ModalityAudio Modality = "audio"
)

// Part is a single segment of input to embed.
type Part struct {
	Modality Modality // text, image, or audio
	Data     []byte   // text as UTF-8, media as raw bytes
	MimeType string   // e.g. "text/plain", "image/png", "audio/wav"
}

// TextPart creates a text-only Part.
func TextPart(text string) Part {
	return Part{Modality: ModalityText, Data: []byte(text), MimeType: "text/plain"}
}

// ImagePart creates an image Part from raw bytes.
func ImagePart(data []byte, mimeType string) Part {
	return Part{Modality: ModalityImage, Data: data, MimeType: mimeType}
}

// Embedder generates embedding vectors from one or more parts.
type Embedder interface {
	// Embed produces a single embedding vector from the given parts.
	Embed(ctx context.Context, parts []Part) ([]float32, error)

	// EmbedBatch produces embedding vectors for multiple inputs at once.
	// Each element in the input slice is a set of parts for one embedding.
	// Returns one vector per input, in the same order.
	EmbedBatch(ctx context.Context, inputs [][]Part) ([][]float32, error)

	// Dimensions returns the dimensionality of the output vectors.
	Dimensions() int

	// Modalities returns the set of modalities this embedder supports.
	Modalities() []Modality
}
