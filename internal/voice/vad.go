package voice

import (
	"encoding/binary"
	"math"
	"time"
)

// VAD detects voice activity in PCM audio streams using energy thresholds.
type VAD struct {
	// SpeechThreshold is the RMS energy level above which audio is considered speech.
	SpeechThreshold float64
	// SilenceDuration is how long silence must last to end a speech segment.
	SilenceDuration time.Duration
	// MinSpeechDuration filters out segments shorter than this.
	MinSpeechDuration time.Duration
	// MaxSpeechDuration forces a speech segment to end after this duration.
	// Zero means no limit.
	MaxSpeechDuration time.Duration

	speaking     bool
	silenceStart time.Time
	speechStart  time.Time
	buffer       []byte
}

// NewVAD creates a VAD with sensible defaults.
func NewVAD() *VAD {
	return &VAD{
		SpeechThreshold:   300,
		SilenceDuration:   800 * time.Millisecond,
		MinSpeechDuration: 300 * time.Millisecond,
	}
}

// VADResult represents the outcome of processing a PCM frame.
type VADResult struct {
	// SpeechEnded is true when a complete speech segment has been detected.
	SpeechEnded bool
	// Audio contains the complete speech segment PCM data (only valid when SpeechEnded).
	Audio []byte
}

// Process feeds a PCM frame (16-bit LE, mono) to the VAD and returns
// a result indicating whether a complete speech segment was detected.
func (v *VAD) Process(pcm []byte, now time.Time) VADResult {
	energy := rmsEnergy(pcm)

	if energy >= v.SpeechThreshold {
		if !v.speaking {
			v.speaking = true
			v.speechStart = now
			v.buffer = v.buffer[:0]
		}
		v.silenceStart = time.Time{} // reset silence timer
		v.buffer = append(v.buffer, pcm...)

		// Force-end if max duration exceeded.
		if v.MaxSpeechDuration > 0 && now.Sub(v.speechStart) >= v.MaxSpeechDuration {
			return v.endSpeech(now)
		}
		return VADResult{}
	}

	// Below threshold (silence).
	if !v.speaking {
		return VADResult{}
	}

	// Still speaking but silent — start or continue silence timer.
	if v.silenceStart.IsZero() {
		v.silenceStart = now
	}
	v.buffer = append(v.buffer, pcm...)

	// Force-end if max duration exceeded.
	if v.MaxSpeechDuration > 0 && now.Sub(v.speechStart) >= v.MaxSpeechDuration {
		return v.endSpeech(now)
	}

	if now.Sub(v.silenceStart) < v.SilenceDuration {
		return VADResult{}
	}

	return v.endSpeech(now)
}

// endSpeech finalizes the current speech segment and returns the result.
func (v *VAD) endSpeech(now time.Time) VADResult {
	v.speaking = false
	duration := now.Sub(v.speechStart)

	if duration < v.MinSpeechDuration {
		v.buffer = v.buffer[:0]
		return VADResult{}
	}

	audio := make([]byte, len(v.buffer))
	copy(audio, v.buffer)
	v.buffer = v.buffer[:0]
	return VADResult{SpeechEnded: true, Audio: audio}
}

// Reset clears the VAD state.
func (v *VAD) Reset() {
	v.speaking = false
	v.silenceStart = time.Time{}
	v.speechStart = time.Time{}
	v.buffer = v.buffer[:0]
}

// rmsEnergy computes the root-mean-square energy of 16-bit LE PCM samples.
func rmsEnergy(pcm []byte) float64 {
	n := len(pcm) / 2
	if n == 0 {
		return 0
	}
	var sum float64
	for i := 0; i < n; i++ {
		sample := int16(binary.LittleEndian.Uint16(pcm[i*2 : i*2+2]))
		sum += float64(sample) * float64(sample)
	}
	return math.Sqrt(sum / float64(n))
}
