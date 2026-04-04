"""Minimal Style-Bert-VITS2 API server."""

import argparse
import io
from pathlib import Path

import torch
# Force float32 globally to avoid fp16/fp32 mismatch between BERT and TTS model.
torch.set_default_dtype(torch.float32)

import numpy as np
import scipy.io.wavfile as wavfile
from fastapi import FastAPI, Query, HTTPException
from fastapi.responses import Response

from style_bert_vits2.constants import Languages
from style_bert_vits2.nlp import bert_models
from style_bert_vits2.tts_model import TTSModel

app = FastAPI()
models: dict[str, TTSModel] = {}


def _try_load_model(d: Path, device: str) -> bool:
    """Try to load a model from directory d. Returns True if loaded."""
    config = d / "config.json"
    if not config.exists():
        return False

    model_file = None
    for ext in ("*.safetensors", "*.pth"):
        found = list(d.glob(ext))
        if found:
            model_file = found[0]
            break
    if model_file is None:
        return False

    style_vec = d / "style_vectors.npy"
    if not style_vec.exists():
        return False

    print(f"Loading model: {d.name}")
    model = TTSModel(
        model_path=model_file,
        config_path=config,
        style_vec_path=style_vec,
        device=device,
    )
    # Ensure float32 to avoid half/float mismatch on fp16 safetensors models.
    if hasattr(model, "net_g") and model.net_g is not None:
        model.net_g = model.net_g.float()
    models[d.name] = model
    print(f"  Loaded: {d.name}")
    return True


def load_models(model_dir: Path, device: str) -> None:
    """Load all models from subdirectories of model_dir (searches up to 2 levels deep)."""
    jp_bert = bert_models.load_model(Languages.JP, "ku-nlp/deberta-v2-large-japanese-char-wwm")
    bert_models.load_tokenizer(Languages.JP, "ku-nlp/deberta-v2-large-japanese-char-wwm")

    # Ensure BERT model is float32 to avoid fp16/fp32 mismatch with TTS model.
    if jp_bert is not None:
        jp_bert.float()

    for d in sorted(model_dir.iterdir()):
        if not d.is_dir() or d.name.startswith("."):
            continue
        if _try_load_model(d, device):
            continue
        # Search one level deeper (e.g. HuggingFace repo structure).
        for sub in sorted(d.iterdir()):
            if sub.is_dir() and not sub.name.startswith("."):
                _try_load_model(sub, device)


@app.get("/models/info")
def models_info():
    return {
        name: {
            "spk2id": model.spk2id if hasattr(model, "spk2id") else {},
            "style2id": model.style2id if hasattr(model, "style2id") else {},
        }
        for name, model in models.items()
    }


@app.post("/voice")
@app.get("/voice")
def voice(
    text: str = Query(..., min_length=1),
    model_name: str = Query(None),
    speaker_id: int = Query(0),
    style: str = Query("Neutral"),
    style_weight: float = Query(1.0),
    language: str = Query("JP"),
    sdp_ratio: float = Query(0.2),
    noise: float = Query(0.6),
    noisew: float = Query(0.8),
    length: float = Query(1.0),
    auto_split: bool = Query(True),
    split_interval: float = Query(0.5),
):
    if not models:
        raise HTTPException(503, "No models loaded")

    if model_name and model_name in models:
        model = models[model_name]
    else:
        model = next(iter(models.values()))

    lang = Languages.JP
    if language == "EN":
        lang = Languages.EN
    elif language == "ZH":
        lang = Languages.ZH

    sr, audio = model.infer(
        text=text,
        language=lang,
        speaker_id=speaker_id,
        style=style,
        style_weight=style_weight,
        sdp_ratio=sdp_ratio,
        noise=noise,
        noise_w=noisew,
        length=length,
        line_split=auto_split,
        split_interval=split_interval,
    )

    buf = io.BytesIO()
    wavfile.write(buf, sr, audio)
    return Response(content=buf.getvalue(), media_type="audio/wav")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--host", default="0.0.0.0")
    parser.add_argument("--port", type=int, default=5000)
    parser.add_argument("--model-dir", type=str, default="/models")
    parser.add_argument("--device", type=str, default="cuda")
    args = parser.parse_args()

    load_models(Path(args.model_dir), args.device)

    if not models:
        print("WARNING: No models found in", args.model_dir)

    import uvicorn

    uvicorn.run(app, host=args.host, port=args.port)


if __name__ == "__main__":
    main()
