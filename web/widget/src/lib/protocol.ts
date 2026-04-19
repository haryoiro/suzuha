// Binary frame types matching firmware/main/config.h and internal/device/device.go
export const FRAME_AUDIO = 0x01; // PCM 24kHz mono (Client -> Server)
export const FRAME_IMAGE = 0x02; // JPEG           (ESP32 -> Server)
export const FRAME_COMMAND = 0x03; // JSON         (Server -> Client)
export const FRAME_STATUS = 0x04; // JSON          (Client -> Server)
export const FRAME_TTS = 0x05; // PCM 24kHz mono   (Server -> Client)
export const FRAME_TTS_END = 0x06; // empty body    (Server -> Client): ストリーム終端マーカー

export interface DecodedFrame {
  type: number;
  payload: ArrayBuffer;
}

export function decodeFrame(data: ArrayBuffer): DecodedFrame {
  const view = new Uint8Array(data);
  return {
    type: view[0],
    payload: data.slice(1),
  };
}

export function encodeAudioFrame(pcm: Int16Array): ArrayBuffer {
  const buf = new ArrayBuffer(1 + pcm.byteLength);
  const view = new Uint8Array(buf);
  view[0] = FRAME_AUDIO;
  new Uint8Array(buf, 1).set(new Uint8Array(pcm.buffer, pcm.byteOffset, pcm.byteLength));
  return buf;
}

export function decodeCommand(payload: ArrayBuffer): Record<string, unknown> {
  const text = new TextDecoder().decode(payload);
  return JSON.parse(text);
}
