FROM ultralytics/ultralytics:latest-cpu

WORKDIR /app
COPY server.py .

RUN pip install --no-cache-dir fastapi uvicorn

# Pre-download model
RUN python -c "from ultralytics import YOLO; YOLO('yolo11n.pt')"

EXPOSE 8002
CMD ["python", "server.py"]
