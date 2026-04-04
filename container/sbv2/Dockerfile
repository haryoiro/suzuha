FROM pytorch/pytorch:2.6.0-cuda12.6-cudnn9-runtime

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends \
    git cmake build-essential libsndfile1 \
    && rm -rf /var/lib/apt/lists/*

RUN pip install --no-cache-dir \
    numpy==1.26.4 \
    style-bert-vits2 \
    fastapi uvicorn[standard] scipy \
    huggingface_hub

COPY entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

COPY server.py /app/server.py

EXPOSE 5000

ENTRYPOINT ["/app/entrypoint.sh"]
