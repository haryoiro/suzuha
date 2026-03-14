"""YOLO object detection sidecar service."""

import io
import time

from fastapi import FastAPI, Request, Response
from PIL import Image
from ultralytics import YOLO

app = FastAPI()
model = YOLO("yolo11n.pt")


@app.post("/detect")
async def detect(request: Request):
    jpeg = await request.body()
    if not jpeg:
        return {"detections": [], "inference_ms": 0}

    img = Image.open(io.BytesIO(jpeg))

    start = time.monotonic()
    results = model(img, verbose=False)
    elapsed_ms = (time.monotonic() - start) * 1000

    detections = []
    for r in results:
        for box in r.boxes:
            x1, y1, x2, y2 = box.xyxy[0].tolist()
            detections.append(
                {
                    "label": r.names[int(box.cls[0])],
                    "confidence": round(float(box.conf[0]), 3),
                    "bbox": [int(x1), int(y1), int(x2), int(y2)],
                }
            )

    return {"detections": detections, "inference_ms": round(elapsed_ms, 1)}


@app.get("/health")
async def health():
    return {"status": "ok"}


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host="0.0.0.0", port=8002)
