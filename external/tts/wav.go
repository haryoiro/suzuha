package tts

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
