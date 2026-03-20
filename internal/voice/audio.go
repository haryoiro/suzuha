package voice

import "encoding/binary"

// wavSampleRate extracts the sample rate from a WAV header.
func wavSampleRate(wav []byte) int {
	if len(wav) < 28 {
		return 0
	}
	return int(binary.LittleEndian.Uint32(wav[24:28]))
}

// wavPCM strips the 44-byte WAV header and returns raw PCM data.
func wavPCM(wav []byte) []byte {
	if len(wav) <= 44 {
		return nil
	}
	return wav[44:]
}

// ResamplePCM resamples 16-bit LE mono PCM from srcRate to dstRate.
// For downsampling, uses averaging (box filter) as anti-aliasing.
// For upsampling, uses linear interpolation.
func ResamplePCM(pcm []byte, srcRate, dstRate int) []byte {
	if srcRate == dstRate {
		return pcm
	}
	srcSamples := len(pcm) / 2
	if srcSamples == 0 {
		return nil
	}
	dstSamples := int(int64(srcSamples) * int64(dstRate) / int64(srcRate))
	out := make([]byte, dstSamples*2)

	ratio := float64(srcRate) / float64(dstRate)

	for i := 0; i < dstSamples; i++ {
		srcCenter := float64(i) * ratio

		var sample int16
		if ratio > 1.0 {
			// Downsampling: average samples in the window [srcCenter-ratio/2, srcCenter+ratio/2]
			lo := int(srcCenter - ratio/2)
			hi := int(srcCenter + ratio/2)
			if lo < 0 {
				lo = 0
			}
			if hi >= srcSamples {
				hi = srcSamples - 1
			}
			sum := int64(0)
			count := 0
			for j := lo; j <= hi; j++ {
				sum += int64(int16(binary.LittleEndian.Uint16(pcm[j*2:])))
				count++
			}
			if count > 0 {
				sample = int16(sum / int64(count))
			}
		} else {
			// Upsampling: linear interpolation
			srcIdx := int(srcCenter)
			frac := srcCenter - float64(srcIdx)
			if srcIdx+1 < srcSamples {
				s0 := int16(binary.LittleEndian.Uint16(pcm[srcIdx*2:]))
				s1 := int16(binary.LittleEndian.Uint16(pcm[(srcIdx+1)*2:]))
				sample = int16(float64(s0)*(1-frac) + float64(s1)*frac)
			} else if srcIdx < srcSamples {
				sample = int16(binary.LittleEndian.Uint16(pcm[srcIdx*2:]))
			}
		}

		binary.LittleEndian.PutUint16(out[i*2:], uint16(sample))
	}
	return out
}

// NormalizePCM scales 16-bit LE mono PCM so the peak reaches targetPeak.
func NormalizePCM(pcm []byte, targetPeak int16) []byte {
	nSamples := len(pcm) / 2
	if nSamples == 0 {
		return pcm
	}

	// Find current peak.
	var maxAbs int16
	for i := 0; i < nSamples; i++ {
		s := int16(binary.LittleEndian.Uint16(pcm[i*2:]))
		if s < 0 {
			s = -s
		}
		if s > maxAbs {
			maxAbs = s
		}
	}

	if maxAbs == 0 || maxAbs >= targetPeak {
		return pcm
	}

	gain := float64(targetPeak) / float64(maxAbs)
	out := make([]byte, len(pcm))
	for i := 0; i < nSamples; i++ {
		s := int16(binary.LittleEndian.Uint16(pcm[i*2:]))
		scaled := int32(float64(s) * gain)
		if scaled > 32767 {
			scaled = 32767
		} else if scaled < -32768 {
			scaled = -32768
		}
		binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(scaled)))
	}
	return out
}

// monoToStereo duplicates a mono 16-bit LE PCM stream to stereo.
func monoToStereo(mono []byte) []byte {
	nSamples := len(mono) / 2
	stereo := make([]byte, nSamples*4) // 2 channels, 2 bytes each
	for i := 0; i < nSamples; i++ {
		sample := mono[i*2 : i*2+2]
		copy(stereo[i*4:], sample)   // left
		copy(stereo[i*4+2:], sample) // right
	}
	return stereo
}
