package voice

import (
	"testing"
	"time"
)

// Typical frame sizes for 48kHz mono 16-bit PCM.
const (
	frameSamples20ms = 960   // 48000 * 0.020
	frameSamples1s   = 48000 // 1 second
)

func BenchmarkRmsEnergy_20ms(b *testing.B) {
	pcm := generateTone(frameSamples20ms, 5000)
	b.SetBytes(int64(len(pcm)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rmsEnergy(pcm)
	}
}

func BenchmarkRmsEnergy_1s(b *testing.B) {
	pcm := generateTone(frameSamples1s, 5000)
	b.SetBytes(int64(len(pcm)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rmsEnergy(pcm)
	}
}

func BenchmarkVADProcess_Speech(b *testing.B) {
	vad := NewVAD()
	pcm := generateTone(frameSamples20ms, 10000)
	now := time.Now()
	b.SetBytes(int64(len(pcm)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vad.Process(pcm, now.Add(time.Duration(i)*20*time.Millisecond))
	}
}

func BenchmarkVADProcess_Silence(b *testing.B) {
	vad := NewVAD()
	pcm := generateSilence(frameSamples20ms)
	now := time.Now()
	b.SetBytes(int64(len(pcm)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vad.Process(pcm, now.Add(time.Duration(i)*20*time.Millisecond))
	}
}

func BenchmarkResample24kTo48k(b *testing.B) {
	// 1 second of 24kHz mono = 24000 samples.
	pcm := generateTone(24000, 5000)
	b.SetBytes(int64(len(pcm)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resample24kTo48k(pcm)
	}
}

func BenchmarkMonoToStereo(b *testing.B) {
	// 1 second of 48kHz mono = 48000 samples.
	pcm := generateTone(frameSamples1s, 5000)
	b.SetBytes(int64(len(pcm)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		monoToStereo(pcm)
	}
}

func BenchmarkPcmToWAV_1s(b *testing.B) {
	pcm := generateTone(frameSamples1s, 5000)
	b.SetBytes(int64(len(pcm)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pcmToWAV(pcm, 48000, 1, 16)
	}
}

func BenchmarkPcmToWAV_5s(b *testing.B) {
	pcm := generateTone(frameSamples1s*5, 5000)
	b.SetBytes(int64(len(pcm)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pcmToWAV(pcm, 48000, 1, 16)
	}
}
