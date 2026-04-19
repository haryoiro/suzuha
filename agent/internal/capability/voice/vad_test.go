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

func TestVAD(t *testing.T) {
	t.Run("detects speech", func(t *testing.T) {
		vad := NewVAD()
		now := time.Now()

		// Feed enough frames of loud audio to exceed MinSpeechDuration (300ms).
		// 20 frames x 20ms = 400ms > 300ms.
		loud := generateTone(960, 10000)
		for i := 0; i < 20; i++ {
			result := vad.Process(loud, now.Add(time.Duration(i)*20*time.Millisecond))
			if result.SpeechEnded {
				t.Fatal("speech should not end during active speech")
			}
		}

		// Feed silence long enough to trigger end (SilenceDuration = 800ms).
		// 50 frames x 20ms = 1000ms > 800ms.
		speechEnd := now.Add(400 * time.Millisecond)
		silent := generateSilence(960)
		for i := 0; i < 50; i++ {
			result := vad.Process(silent, speechEnd.Add(time.Duration(i)*20*time.Millisecond))
			if result.SpeechEnded {
				if len(result.Audio) == 0 {
					t.Fatal("speech ended but audio is empty")
				}
				return
			}
		}
		t.Fatal("speech segment was never detected")
	})

	t.Run("ignores short speech", func(t *testing.T) {
		vad := NewVAD()
		vad.MinSpeechDuration = 2 * time.Second
		vad.SilenceDuration = 100 * time.Millisecond
		now := time.Now()

		loud := generateTone(960, 10000)
		vad.Process(loud, now)

		silent := generateSilence(960)
		for i := 0; i < 20; i++ {
			result := vad.Process(silent, now.Add(20*time.Millisecond+time.Duration(i)*20*time.Millisecond))
			if result.SpeechEnded {
				t.Fatal("short speech should be ignored")
			}
		}
	})

	t.Run("reset", func(t *testing.T) {
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
	})

	t.Run("max speech duration", func(t *testing.T) {
		vad := NewVAD()
		vad.MaxSpeechDuration = 500 * time.Millisecond
		vad.MinSpeechDuration = 100 * time.Millisecond
		now := time.Now()

		loud := generateTone(960, 10000)
		for i := 0; i < 40; i++ {
			result := vad.Process(loud, now.Add(time.Duration(i)*20*time.Millisecond))
			if result.SpeechEnded {
				if len(result.Audio) == 0 {
					t.Fatal("speech ended but audio is empty")
				}
				return
			}
		}
		t.Fatal("max speech duration should have forced speech end")
	})
}

func TestRmsEnergy(t *testing.T) {
	tests := []struct {
		name    string
		buf     []byte
		checkFn func(*testing.T, float64)
	}{
		{
			"silence",
			generateSilence(100),
			func(t *testing.T, energy float64) {
				t.Helper()
				if energy != 0 {
					t.Errorf("silence energy should be 0, got %f", energy)
				}
			},
		},
		{
			"loud tone",
			generateTone(100, 20000),
			func(t *testing.T, energy float64) {
				t.Helper()
				if energy < 1000 {
					t.Errorf("loud energy should be high, got %f", energy)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.checkFn(t, rmsEnergy(tt.buf))
		})
	}
}
