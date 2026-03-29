FROM python:3.11-slim

WORKDIR /app

RUN pip install --no-cache-dir \
    fastapi uvicorn onnxruntime numpy opencv-python-headless

# Download PINTO0309 YOLOX-Body-Head-Hand-Face (Nano, 384x640, NMS baked in)
RUN apt-get update && apt-get install -y --no-install-recommends curl && \
    curl -L "https://s3.ap-northeast-2.wasabisys.com/pinto-model-zoo/434_YOLOX-Body-Head-Hand-Face/resources_n.tar.gz" \
      -o /tmp/resources.tar.gz && \
    tar -xzf /tmp/resources.tar.gz -C /tmp && \
    cp /tmp/yolox_n_body_head_hand_face_0299_0.3803_post_1x3x384x640.onnx /app/model.onnx && \
    rm -rf /tmp/resources.tar.gz /tmp/yolox_* /tmp/demo* && \
    apt-get remove -y curl && apt-get autoremove -y && rm -rf /var/lib/apt/lists/*

COPY server.py .

EXPOSE 8002
CMD ["python", "server.py"]
