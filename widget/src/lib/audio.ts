// Audio format conversion utilities for WebSocket binary protocol.
// Server uses 24kHz PCM16 (Int16); Web Audio API uses native sample rate (typically 48kHz) Float32.

export const DEVICE_SAMPLE_RATE = 24000;

/** Convert Float32 audio samples [-1, 1] to Int16 PCM [-32768, 32767]. */
export function float32ToInt16(input: Float32Array): Int16Array {
  const output = new Int16Array(input.length);
  for (let i = 0; i < input.length; i++) {
    const s = Math.max(-1, Math.min(1, input[i]));
    output[i] = s < 0 ? s * 0x8000 : s * 0x7fff;
  }
  return output;
}

/** Convert Int16 PCM to Float32 samples [-1, 1]. */
export function int16ToFloat32(input: Int16Array): Float32Array {
  const output = new Float32Array(input.length);
  for (let i = 0; i < input.length; i++) {
    output[i] = input[i] / 0x8000;
  }
  return output;
}

/** Simple linear interpolation resample. */
export function resample(
  pcm: Int16Array,
  srcRate: number,
  dstRate: number
): Int16Array {
  if (srcRate === dstRate) return pcm;
  const ratio = srcRate / dstRate;
  const outLen = Math.round(pcm.length / ratio);
  const output = new Int16Array(outLen);
  for (let i = 0; i < outLen; i++) {
    const srcIdx = i * ratio;
    const idx0 = Math.floor(srcIdx);
    const idx1 = Math.min(idx0 + 1, pcm.length - 1);
    const frac = srcIdx - idx0;
    output[i] = Math.round(pcm[idx0] * (1 - frac) + pcm[idx1] * frac);
  }
  return output;
}

/** Resample Float32 audio from srcRate to dstRate. */
export function resampleFloat32(
  data: Float32Array,
  srcRate: number,
  dstRate: number
): Float32Array {
  if (srcRate === dstRate) return data;
  const ratio = srcRate / dstRate;
  const outLen = Math.round(data.length / ratio);
  const output = new Float32Array(outLen);
  for (let i = 0; i < outLen; i++) {
    const srcIdx = i * ratio;
    const idx0 = Math.floor(srcIdx);
    const idx1 = Math.min(idx0 + 1, data.length - 1);
    const frac = srcIdx - idx0;
    output[i] = data[idx0] * (1 - frac) + data[idx1] * frac;
  }
  return output;
}
