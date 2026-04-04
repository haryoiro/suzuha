#!/bin/bash
set -e

MODEL_DIR="/models/${SBV2_MODEL_NAME:-koharune_ami}"

# Download model from HuggingFace if not present.
if [ ! -d "$MODEL_DIR" ] || [ -z "$(ls -A "$MODEL_DIR" 2>/dev/null)" ]; then
    echo "Downloading model: ${SBV2_MODEL_REPO:-litagin/sbv2_koharune_ami} -> $MODEL_DIR"
    python -c "
from huggingface_hub import snapshot_download
snapshot_download('${SBV2_MODEL_REPO:-litagin/sbv2_koharune_ami}', local_dir='$MODEL_DIR')
"
fi

# Download BERT models if not cached.
python -c "
from style_bert_vits2.nlp import bert_models
from style_bert_vits2.constants import Languages
print('Loading JP BERT model...')
bert_models.load_model(Languages.JP, 'ku-nlp/deberta-v2-large-japanese-char-wwm')
bert_models.load_tokenizer(Languages.JP, 'ku-nlp/deberta-v2-large-japanese-char-wwm')
print('BERT models ready.')
"

exec python /app/server.py \
    --host 0.0.0.0 \
    --port "${SBV2_PORT:-5000}" \
    --model-dir /models
