import { useRef, useState, useCallback } from "react";
import {
  float32ToInt16,
  int16ToFloat32,
  resampleFloat32,
  DEVICE_SAMPLE_RATE,
} from "../lib/audio";

interface UseAudioOptions {
  onAudioChunk: (pcm: Int16Array) => void;
  onPlaybackStart?: () => void;
  onPlaybackEnd?: () => void;
}

export function useAudio({ onAudioChunk, onPlaybackStart, onPlaybackEnd }: UseAudioOptions) {
  const [micActive, setMicActive] = useState(false);
  const [playing, setPlaying] = useState(false);

  const audioCtxRef = useRef<AudioContext | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const workletRef = useRef<AudioWorkletNode | ScriptProcessorNode | null>(null);
  const ttsQueueRef = useRef<Int16Array[]>([]);
  const playingRef = useRef(false);
  const onChunkRef = useRef(onAudioChunk);
  onChunkRef.current = onAudioChunk;
  const onPlayStartRef = useRef(onPlaybackStart);
  onPlayStartRef.current = onPlaybackStart;
  const onPlayEndRef = useRef(onPlaybackEnd);
  onPlayEndRef.current = onPlaybackEnd;

  const getAudioContext = useCallback(() => {
    if (!audioCtxRef.current) {
      audioCtxRef.current = new AudioContext();
    }
    return audioCtxRef.current;
  }, []);

  const startMic = useCallback(async () => {
    const ctx = getAudioContext();
    if (ctx.state === "suspended") await ctx.resume();

    const stream = await navigator.mediaDevices.getUserMedia({
      audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true },
    });
    streamRef.current = stream;

    const source = ctx.createMediaStreamSource(stream);
    const nativeSR = ctx.sampleRate;

    // Use ScriptProcessorNode for broad compatibility
    // Buffer size ~100ms at native rate
    const bufSize = Math.pow(2, Math.ceil(Math.log2(nativeSR * 0.1)));
    const processor = ctx.createScriptProcessor(bufSize, 1, 1);

    processor.onaudioprocess = (e) => {
      if (playingRef.current) return; // Mute mic during TTS playback

      const input = e.inputBuffer.getChannelData(0);
      // Resample from native rate to 24kHz
      const resampled = resampleFloat32(input, nativeSR, DEVICE_SAMPLE_RATE);
      const pcm16 = float32ToInt16(resampled);
      onChunkRef.current(pcm16);
    };

    source.connect(processor);
    processor.connect(ctx.destination); // Required for ScriptProcessor to fire
    workletRef.current = processor;

    setMicActive(true);
  }, [getAudioContext]);

  const stopMic = useCallback(() => {
    if (workletRef.current) {
      workletRef.current.disconnect();
      workletRef.current = null;
    }
    if (streamRef.current) {
      streamRef.current.getTracks().forEach((t) => t.stop());
      streamRef.current = null;
    }
    setMicActive(false);
  }, []);

  const toggleMic = useCallback(async () => {
    if (micActive) {
      stopMic();
    } else {
      await startMic();
    }
  }, [micActive, startMic, stopMic]);

  // TTS playback: queue chunks and play them sequentially
  const enqueueTTS = useCallback(
    (payload: ArrayBuffer) => {
      const pcm16 = new Int16Array(
        payload.slice(0, payload.byteLength - (payload.byteLength % 2))
      );
      ttsQueueRef.current.push(pcm16);

      if (!playingRef.current) {
        playNextTTS();
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    []
  );

  const playNextTTS = useCallback(() => {
    if (ttsQueueRef.current.length === 0) {
      playingRef.current = false;
      setPlaying(false);
      onPlayEndRef.current?.();
      return;
    }

    if (!playingRef.current) {
      playingRef.current = true;
      setPlaying(true);
      onPlayStartRef.current?.();
    }

    // Concatenate all queued chunks
    let totalLen = 0;
    for (const chunk of ttsQueueRef.current) totalLen += chunk.length;
    const combined = new Int16Array(totalLen);
    let offset = 0;
    for (const chunk of ttsQueueRef.current) {
      combined.set(chunk, offset);
      offset += chunk.length;
    }
    ttsQueueRef.current = [];

    const ctx = getAudioContext();
    const nativeSR = ctx.sampleRate;

    // Convert to Float32 and resample from 24kHz to native rate
    const float32 = int16ToFloat32(combined);
    const resampled = resampleFloat32(float32, DEVICE_SAMPLE_RATE, nativeSR);

    const buffer = ctx.createBuffer(1, resampled.length, nativeSR);
    buffer.getChannelData(0).set(resampled);

    const source = ctx.createBufferSource();
    source.buffer = buffer;
    source.connect(ctx.destination);
    source.onended = () => {
      // Check if more chunks arrived during playback
      if (ttsQueueRef.current.length > 0) {
        playNextTTS();
      } else {
        playingRef.current = false;
        setPlaying(false);
        onPlayEndRef.current?.();
      }
    };
    source.start();
  }, [getAudioContext]);

  return { micActive, playing, toggleMic, enqueueTTS };
}
