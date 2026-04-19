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
  // 直近のマイク入力 RMS (0–1)。UI のレベルメータ用に更新する。
  const [voiceLevel, setVoiceLevel] = useState(0);

  const audioCtxRef = useRef<AudioContext | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const workletRef = useRef<AudioWorkletNode | ScriptProcessorNode | null>(null);
  const playingRef = useRef(false);
  // micActive の状態を同期 ref (再生終了時に再取得すべきか判定する)。
  const micActiveRef = useRef(false);
  // 次に音声を差し込むべき AudioContext 時刻 (ギャップレス再生のため前の buffer 終端に連結)。
  const playbackEndTimeRef = useRef(0);
  // ストリーム終端 (FRAME_TTS_END) を受け取ったかどうか。
  const streamEndedRef = useRef(false);
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

  // 実際にマイクストリームを取得してパイプラインを組み立てる (内部実装)。
  // ユーザー操作で呼ばれる startMic と、bot 発話終了後の再開の両方から使う。
  const acquireMicStream = useCallback(async () => {
    const ctx = getAudioContext();
    if (ctx.state === "suspended") await ctx.resume();

    // iOS Safari の VoiceChat セッション切替を避けるため DSP は全部切る。
    const stream = await navigator.mediaDevices.getUserMedia({
      audio: {
        echoCancellation: false,
        noiseSuppression: false,
        autoGainControl: false,
      },
    });
    streamRef.current = stream;

    const source = ctx.createMediaStreamSource(stream);
    const nativeSR = ctx.sampleRate;

    const bufSize = Math.pow(2, Math.ceil(Math.log2(nativeSR * 0.1)));
    const processor = ctx.createScriptProcessor(bufSize, 1, 1);

    // UI 更新は頻度を抑える (onaudioprocess は数十Hz で発火するため)。
    let lastLevelUpdate = 0;

    processor.onaudioprocess = (e) => {
      if (playingRef.current) return;

      const input = e.inputBuffer.getChannelData(0);

      // RMS 計算 → UI の voiceLevel を 60ms 間隔で更新。
      const now = performance.now();
      if (now - lastLevelUpdate > 60) {
        let sum = 0;
        for (let i = 0; i < input.length; i++) sum += input[i] * input[i];
        const rms = Math.sqrt(sum / input.length);
        // 音声範囲 0-0.3 を 0-1 にマッピング (スピーチは大抵 ~0.1 程度)。
        setVoiceLevel(Math.min(1, rms / 0.3));
        lastLevelUpdate = now;
      }

      const resampled = resampleFloat32(input, nativeSR, DEVICE_SAMPLE_RATE);
      const pcm16 = float32ToInt16(resampled);
      onChunkRef.current(pcm16);
    };

    source.connect(processor);
    processor.connect(ctx.destination);
    workletRef.current = processor;
  }, [getAudioContext]);

  // マイクストリームだけを解放 (MediaStreamTrack.stop + processor disconnect)。
  // iOS Safari は getUserMedia が生きてる間 audio session を PlayAndRecord に張り付かせて
  // Bluetooth を HFP (モノ・電話帯域) に降格するため、bot 発話中は release する。
  const releaseMicStream = useCallback(() => {
    if (workletRef.current) {
      workletRef.current.disconnect();
      workletRef.current = null;
    }
    if (streamRef.current) {
      streamRef.current.getTracks().forEach((t) => t.stop());
      streamRef.current = null;
    }
    setVoiceLevel(0);
  }, []);

  const startMic = useCallback(async () => {
    // 念のため再生ステートをクリア (前回の bot 発話が変な状態で残ってたら困るため)。
    playingRef.current = false;
    setPlaying(false);
    streamEndedRef.current = false;
    playbackEndTimeRef.current = 0;
    await acquireMicStream();
    micActiveRef.current = true;
    setMicActive(true);
  }, [acquireMicStream]);

  const stopMic = useCallback(() => {
    releaseMicStream();
    micActiveRef.current = false;
    setMicActive(false);
  }, [releaseMicStream]);

  const toggleMic = useCallback(async () => {
    if (micActive) {
      stopMic();
    } else {
      await startMic();
    }
  }, [micActive, startMic, stopMic]);

  // TTS playback: チャンクを受信するたびに AudioContext 時刻にスケジュールしてギャップレス再生。
  const enqueueTTS = useCallback(
    (payload: ArrayBuffer) => {
      const pcm16 = new Int16Array(
        payload.slice(0, payload.byteLength - (payload.byteLength % 2))
      );
      if (pcm16.length === 0) return;

      const ctx = getAudioContext();
      // iOS: suspended 状態なら resume (bot 発話中に audio context が sleep してる場合がある)。
      if (ctx.state === "suspended") ctx.resume().catch(() => {});
      const nativeSR = ctx.sampleRate;

      const float32 = int16ToFloat32(pcm16);
      const resampled = resampleFloat32(float32, DEVICE_SAMPLE_RATE, nativeSR);

      const buffer = ctx.createBuffer(1, resampled.length, nativeSR);
      buffer.getChannelData(0).set(resampled);

      const source = ctx.createBufferSource();
      source.buffer = buffer;
      source.connect(ctx.destination);

      const startTime = Math.max(ctx.currentTime + 0.01, playbackEndTimeRef.current);
      source.start(startTime);
      playbackEndTimeRef.current = startTime + buffer.duration;
      streamEndedRef.current = false;

      if (!playingRef.current) {
        playingRef.current = true;
        setPlaying(true);
        onPlayStartRef.current?.();
      }

      source.onended = () => {
        if (streamEndedRef.current && ctx.currentTime >= playbackEndTimeRef.current - 0.02) {
          playingRef.current = false;
          setPlaying(false);
          onPlayEndRef.current?.();
        }
      };
    },
    [getAudioContext]
  );

  // ストリーム終端マーカー受信。最後の buffer が再生し終わったら onPlaybackEnd を発火。
  const endTTS = useCallback(() => {
    streamEndedRef.current = true;
    const ctx = audioCtxRef.current;
    if (ctx && ctx.currentTime >= playbackEndTimeRef.current && playingRef.current) {
      playingRef.current = false;
      setPlaying(false);
      onPlayEndRef.current?.();
    }
  }, []);

  return { micActive, playing, voiceLevel, toggleMic, enqueueTTS, endTTS };
}
