package stt

// pcmToWAV wraps raw PCM data in a minimal WAV header.
func pcmToWAV(pcm []byte, sampleRate, channels, bitsPerSample int) []byte {
	dataSize := len(pcm)
	byteRate := sampleRate * channels * bitsPerSample / 8
	blockAlign := channels * bitsPerSample / 8

	buf := make([]byte, 44+dataSize)
	copy(buf[0:4], "RIFF")
	le32(buf[4:8], uint32(36+dataSize))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	le32(buf[16:20], 16) // PCM format chunk size
	le16(buf[20:22], 1)  // PCM format
	le16(buf[22:24], uint16(channels))
	le32(buf[24:28], uint32(sampleRate))
	le32(buf[28:32], uint32(byteRate))
	le16(buf[32:34], uint16(blockAlign))
	le16(buf[34:36], uint16(bitsPerSample))
	copy(buf[36:40], "data")
	le32(buf[40:44], uint32(dataSize))
	copy(buf[44:], pcm)
	return buf
}

func le16(b []byte, v uint16) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
}

func le32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}
