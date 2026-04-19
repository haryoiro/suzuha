import { useEffect, useRef, useState } from "react";

interface FaceProps {
  expression: number;
  size?: number;
}

// NONO のピクセルポートレート。expression は現状見た目に反映しない (素材が 1 枚)。
// 細かい表情変化を将来足すなら expression 別画像を public/nono-*.png として追加する。
export default function Face({ expression, size = 256 }: FaceProps) {
  const imgRef = useRef<HTMLImageElement>(null);
  // 「話してる (7)」の時だけ軽く上下に弾ませて生きてる感を出す。
  const [talking, setTalking] = useState(false);
  useEffect(() => {
    setTalking(expression === 7);
  }, [expression]);

  return (
    <div
      style={{
        width: size,
        height: size,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
      }}
    >
      <img
        ref={imgRef}
        src="/nono.png"
        alt="NONO"
        width={size}
        height={size}
        style={{
          width: size,
          height: size,
          // ピクセル感を保つ: canvas と同じで補間させない。
          imageRendering: "pixelated",
          display: "block",
          animation: talking ? "nonoBounce 0.48s ease-in-out infinite" : undefined,
        }}
      />
    </div>
  );
}
