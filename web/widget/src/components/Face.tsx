import { useRef, useEffect, useCallback } from "react";
import { EXPRESSIONS, type FaceParams } from "../lib/face-params";

interface FaceProps {
  expression: number;
  size?: number;
}

// Lerp helper for smooth transitions
function lerp(a: number, b: number, t: number): number {
  return a + (b - a) * t;
}

function lerpParams(a: FaceParams, b: FaceParams, t: number): FaceParams {
  return {
    eyeW: lerp(a.eyeW, b.eyeW, t),
    eyeH: lerp(a.eyeH, b.eyeH, t),
    eyeYOffset: lerp(a.eyeYOffset, b.eyeYOffset, t),
    mouthW: lerp(a.mouthW, b.mouthW, t),
    mouthH: lerp(a.mouthH, b.mouthH, t),
    mouthOpen: t > 0.5 ? b.mouthOpen : a.mouthOpen,
  };
}

export default function Face({ expression, size = 256 }: FaceProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const currentParams = useRef<FaceParams>(EXPRESSIONS[0]);
  const targetExpr = useRef(0);
  const blinkProgress = useRef(0); // 0 = open, 1 = closed
  const blinkTimer = useRef(0);
  const transitionProgress = useRef(1);
  const prevParams = useRef<FaceParams>(EXPRESSIONS[0]);
  const animFrameId = useRef(0);
  const lastTime = useRef(0);

  // Reference size from firmware: 128px
  const scale = size / 128;

  const drawFace = useCallback(
    (ctx: CanvasRenderingContext2D, params: FaceParams, blink: number) => {
      const s = scale;
      ctx.clearRect(0, 0, size, size);
      ctx.fillStyle = "#000";
      ctx.fillRect(0, 0, size, size);

      const cx = size / 2;
      const cy = size / 2 - 8 * s;
      const eyeSpacing = 20 * s;

      // Apply blink: shrink eye height
      const effectiveEyeH = params.eyeH * (1 - blink * 0.9) * s;
      const eyeW = params.eyeW * s;

      // Draw eyes as filled ellipses
      ctx.fillStyle = "#fff";
      for (const side of [-1, 1]) {
        const ex = cx + side * eyeSpacing;
        const ey = cy + params.eyeYOffset * s;
        ctx.beginPath();
        ctx.ellipse(ex, ey, eyeW, Math.max(effectiveEyeH, 1), 0, 0, Math.PI * 2);
        ctx.fill();
      }

      // Draw mouth as rounded rectangle
      const mouthY = cy + 25 * s;
      const mw = params.mouthW * s;
      const mh = params.mouthH * s;
      const radius = params.mouthOpen ? (mh / 2) : Math.min(1 * s, mh / 2);

      ctx.beginPath();
      ctx.roundRect(cx - mw / 2, mouthY, mw, mh, radius);
      ctx.fill();
    },
    [size, scale]
  );

  useEffect(() => {
    const validExpr = Math.max(0, Math.min(7, expression));
    if (validExpr !== targetExpr.current) {
      prevParams.current = { ...currentParams.current };
      targetExpr.current = validExpr;
      transitionProgress.current = 0;
    }
  }, [expression]);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    lastTime.current = performance.now();

    const animate = (now: number) => {
      const dt = (now - lastTime.current) / 1000;
      lastTime.current = now;

      // Expression transition (200ms)
      if (transitionProgress.current < 1) {
        transitionProgress.current = Math.min(1, transitionProgress.current + dt / 0.2);
      }

      const target = EXPRESSIONS[targetExpr.current];
      const params = transitionProgress.current >= 1
        ? target
        : lerpParams(prevParams.current, target, transitionProgress.current);
      currentParams.current = params;

      // Blink animation
      blinkTimer.current -= dt;
      if (blinkTimer.current <= 0) {
        if (blinkProgress.current === 0) {
          // Start blink
          blinkProgress.current = 0.01;
          blinkTimer.current = 0.1; // blink duration
        } else {
          // End blink, schedule next
          blinkProgress.current = 0;
          blinkTimer.current = 3 + Math.random() * 4; // 3-7s interval
        }
      }

      // Smooth blink
      if (blinkProgress.current > 0 && blinkProgress.current < 1) {
        blinkProgress.current = Math.min(1, blinkProgress.current + dt / 0.08);
      }
      const blink = blinkProgress.current > 0.5
        ? 1 - (blinkProgress.current - 0.5) * 2
        : blinkProgress.current * 2;

      drawFace(ctx, params, blink);
      animFrameId.current = requestAnimationFrame(animate);
    };

    // Initialize blink timer
    blinkTimer.current = 2 + Math.random() * 3;
    animFrameId.current = requestAnimationFrame(animate);

    return () => cancelAnimationFrame(animFrameId.current);
  }, [drawFace]);

  return (
    <canvas
      ref={canvasRef}
      width={size}
      height={size}
      style={{ width: size, height: size, display: "block" }}
    />
  );
}
