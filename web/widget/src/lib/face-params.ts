// Face expression parameters ported from firmware/main/display_task.c s_faces[] array.
// Original renders on SSD1351 128x128 OLED; here we scale to arbitrary canvas size.

export interface FaceParams {
  eyeW: number; // ellipse half-width
  eyeH: number; // ellipse half-height
  eyeYOffset: number; // vertical shift from center (negative = up)
  mouthW: number; // mouth width
  mouthH: number; // mouth height
  mouthOpen: boolean;
}

export const EXPRESSIONS: FaceParams[] = [
  /* 0 neutral   */ { eyeW: 6, eyeH: 6, eyeYOffset: 0, mouthW: 10, mouthH: 2, mouthOpen: false },
  /* 1 happy     */ { eyeW: 6, eyeH: 4, eyeYOffset: -2, mouthW: 14, mouthH: 5, mouthOpen: false },
  /* 2 sad       */ { eyeW: 5, eyeH: 7, eyeYOffset: 2, mouthW: 8, mouthH: 4, mouthOpen: true },
  /* 3 surprised */ { eyeW: 8, eyeH: 8, eyeYOffset: 0, mouthW: 12, mouthH: 8, mouthOpen: true },
  /* 4 angry     */ { eyeW: 7, eyeH: 5, eyeYOffset: -1, mouthW: 10, mouthH: 3, mouthOpen: false },
  /* 5 sleepy    */ { eyeW: 6, eyeH: 3, eyeYOffset: 0, mouthW: 8, mouthH: 2, mouthOpen: false },
  /* 6 thinking  */ { eyeW: 5, eyeH: 5, eyeYOffset: -3, mouthW: 6, mouthH: 2, mouthOpen: false },
  /* 7 talking   */ { eyeW: 6, eyeH: 6, eyeYOffset: 0, mouthW: 10, mouthH: 7, mouthOpen: true },
];

export const EXPRESSION_NAMES = [
  "neutral",
  "happy",
  "sad",
  "surprised",
  "angry",
  "sleepy",
  "thinking",
  "talking",
] as const;

export type ExpressionName = (typeof EXPRESSION_NAMES)[number];
