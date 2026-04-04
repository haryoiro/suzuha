"""YOLOX-Body-Head-Hand-Face detection sidecar service.

Model: PINTO0309/PINTO_model_zoo #434 (YOLOX-Nano, NMS baked in)
Classes: 0=body, 1=head, 2=hand, 3=face
"""

import io
import os
import time

import cv2
import numpy as np
import onnxruntime
from fastapi import FastAPI, Request

CLASS_NAMES = {0: "body", 1: "head", 2: "hand", 3: "face"}
SCORE_THRESHOLD = float(os.getenv("SCORE_THRESHOLD", "0.35"))

app = FastAPI()

MODEL_PATH = os.getenv("MODEL_PATH", "/app/model.onnx")
session = onnxruntime.InferenceSession(MODEL_PATH, providers=["CPUExecutionProvider"])
input_info = session.get_inputs()[0]
input_name = input_info.name
_, _, MODEL_H, MODEL_W = input_info.shape


@app.post("/detect")
async def detect(request: Request):
    jpeg = await request.body()
    if not jpeg:
        return {"detections": [], "inference_ms": 0}

    # Decode JPEG -> BGR numpy array
    buf = np.frombuffer(jpeg, dtype=np.uint8)
    img = cv2.imdecode(buf, cv2.IMREAD_COLOR)
    if img is None:
        return {"detections": [], "inference_ms": 0}

    img_h, img_w = img.shape[:2]

    # Preprocess: resize, HWC->CHW, 0-255 float32 (no normalization)
    resized = cv2.resize(img, (MODEL_W, MODEL_H))
    tensor = resized.transpose(2, 0, 1).astype(np.float32)[np.newaxis]

    start = time.monotonic()
    outputs = session.run(None, {input_name: tensor})
    elapsed_ms = (time.monotonic() - start) * 1000

    detections = []
    for det in outputs[0]:
        _, class_id, score, x1, y1, x2, y2 = det
        if score < SCORE_THRESHOLD:
            continue
        # Scale from model coords to original image coords
        detections.append({
            "label": CLASS_NAMES.get(int(class_id), "unknown"),
            "confidence": round(float(score), 3),
            "bbox": [
                int(max(0, x1) * img_w / MODEL_W),
                int(max(0, y1) * img_h / MODEL_H),
                int(min(x2, MODEL_W) * img_w / MODEL_W),
                int(min(y2, MODEL_H) * img_h / MODEL_H),
            ],
        })

    return {"detections": detections, "inference_ms": round(elapsed_ms, 1)}


@app.get("/health")
async def health():
    return {"status": "ok", "model": MODEL_PATH, "input": f"{MODEL_H}x{MODEL_W}"}


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8002)
