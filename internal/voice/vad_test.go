package voice

import (
	"encoding/binary"
	"math"
	"testing"
	"time"
)

// generateTone creates a 16-bit LE mono PCM buffer with a sine wave at the given amplitude.
func generateTone(samples int, amplitude float64) []byte {
	buf := make([]byte, samples*2)
	for i := 0; i < samples; i++ {
		val := int16(amplitude * math.Sin(2*math.Pi*float64(i)/float64(samples)))
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(val))
	}
	return buf
}

// generateSilence creates a silent PCM buffer.
func generateSilence(samples int) []byte {
	return make([]byte, samples*2)
}

func TestVAD_DetectsSpeech(t *testing.T) {
	vad := NewVAD()
	now := time.Now()

	// Feed enough frames of loud audio to exceed MinSpeechDuration (300ms).
	// 20 frames × 20ms = 400ms > 300ms.
	loud := generateTone(960, 10000) // well above threshold
	for i := 0; i < 20; i++ {
		result := vad.Process(loud, now.Add(time.Duration(i)*20*time.Millisecond))
		if result.SpeechEnded {
			t.Fatal("speech should not end during active speech")
		}
	}

	// Feed silence long enough to trigger end (SilenceDuration = 800ms).
	// 50 frames × 20ms = 1000ms > 800ms.
	speechEnd := now.Add(400 * time.Millisecond)
	silent := generateSilence(960)
	for i := 0; i < 50; i++ {
		result := vad.Process(silent, speechEnd.Add(time.Duration(i)*20*time.Millisecond))
		if result.SpeechEnded {
			if len(result.Audio) == 0 {
				t.Fatal("speech ended but audio is empty")
			}
			return // success
		}
	}
	t.Fatal("speech segment was never detected")
}

func TestVAD_IgnoresShortSpeech(t *testing.T) {
	vad := NewVAD()
	vad.MinSpeechDuration = 2 * time.Second // require 2s of speech
	vad.SilenceDuration = 100 * time.Millisecond
	now := time.Now()

	// Very short burst of speech (1 frame = 20ms).
	loud := generateTone(960, 10000)
	vad.Process(loud, now)

	// Immediately go silent — silence timer should end speech quickly,
	// but the segment is too short and should be discarded.
	silent := generateSilence(960)
	for i := 0; i < 20; i++ {
		result := vad.Process(silent, now.Add(20*time.Millisecond+time.Duration(i)*20*time.Millisecond))
		if result.SpeechEnded {
			t.Fatal("short speech should be ignored")
		}
	}
}

func TestVAD_Reset(t *testing.T) {
	vad := NewVAD()
	now := time.Now()

	loud := generateTone(960, 10000)
	vad.Process(loud, now)

	if !vad.speaking {
		t.Fatal("should be speaking after loud input")
	}

	vad.Reset()

	if vad.speaking {
		t.Fatal("should not be speaking after reset")
	}
}

func TestRmsEnergy(t *testing.T) {
	silent := generateSilence(100)
	if e := rmsEnergy(silent); e != 0 {
		t.Errorf("silence energy should be 0, got %f", e)
	}

	loud := generateTone(100, 20000)
	if e := rmsEnergy(loud); e < 1000 {
		t.Errorf("loud energy should be high, got %f", e)
	}
}
