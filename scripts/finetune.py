#!/usr/bin/env python3
"""Fine-tune Qwen3.5-4B with Unsloth QLoRA on conversation logs.

Usage:
    # Activate venv first
    source .venv-ft/bin/activate
    python scripts/finetune.py

    # With custom settings
    python scripts/finetune.py --epochs 5 --lr 1e-4 --rank 32

    # Export to GGUF after training
    python scripts/finetune.py --export-gguf
"""

import argparse
import os


def main():
    parser = argparse.ArgumentParser(description="Fine-tune Qwen3.5-4B")
    parser.add_argument("--data", default="data/finetune/train.jsonl", help="Training data path")
    parser.add_argument("--output", default="data/finetune/output", help="Output directory")
    parser.add_argument("--adapter-dir", default="data/finetune/lora-adapter", help="LoRA adapter save path")
    parser.add_argument("--model", default="unsloth/Qwen3.5-4B", help="Base model")
    parser.add_argument("--max-seq-len", type=int, default=2048, help="Max sequence length")
    parser.add_argument("--epochs", type=int, default=3, help="Number of training epochs")
    parser.add_argument("--lr", type=float, default=2e-4, help="Learning rate")
    parser.add_argument("--batch-size", type=int, default=1, help="Per-device batch size")
    parser.add_argument("--grad-accum", type=int, default=4, help="Gradient accumulation steps")
    parser.add_argument("--rank", type=int, default=16, help="LoRA rank")
    parser.add_argument("--export-gguf", action="store_true", help="Export to GGUF after training")
    parser.add_argument("--gguf-dir", default="data/finetune/gguf", help="GGUF export directory")
    parser.add_argument("--gguf-quant", default="q4_k_m", help="GGUF quantization method")
    args = parser.parse_args()

    # Suppress tokenizer warnings
    os.environ.setdefault("TOKENIZERS_PARALLELISM", "false")

    from unsloth import FastLanguageModel
    from unsloth.chat_templates import get_chat_template
    from datasets import load_dataset
    from trl import SFTTrainer
    from transformers import TrainingArguments

    print(f"Loading model: {args.model}")
    model, tokenizer = FastLanguageModel.from_pretrained(
        model_name=args.model,
        max_seq_length=args.max_seq_len,
        load_in_4bit=True,
    )

    # Apply LoRA
    print(f"Applying LoRA (rank={args.rank})")
    model = FastLanguageModel.get_peft_model(
        model,
        r=args.rank,
        lora_alpha=args.rank,
        target_modules=[
            "q_proj", "k_proj", "v_proj", "o_proj",
            "gate_proj", "up_proj", "down_proj",
        ],
        lora_dropout=0,
        bias="none",
        use_gradient_checkpointing="unsloth",
        random_state=42,
    )

    # Set up chat template (Qwen uses ChatML)
    tokenizer = get_chat_template(tokenizer, chat_template="qwen-2.5")

    # Load dataset
    print(f"Loading dataset: {args.data}")
    dataset = load_dataset("json", data_files=args.data, split="train")
    print(f"Dataset size: {len(dataset)} examples")

    # Format function for SFTTrainer
    def formatting_func(examples):
        texts = []
        for messages in examples["messages"]:
            text = tokenizer.apply_chat_template(
                messages, tokenize=False, add_generation_prompt=False
            )
            texts.append(text)
        return {"text": texts}

    dataset = dataset.map(formatting_func, batched=True, remove_columns=dataset.column_names)

    # Training
    # Note: trl >= 0.16 uses processing_class instead of tokenizer
    print("Starting training...")
    trainer = SFTTrainer(
        model=model,
        processing_class=tokenizer,
        train_dataset=dataset,
        args=TrainingArguments(
            output_dir=args.output,
            per_device_train_batch_size=args.batch_size,
            gradient_accumulation_steps=args.grad_accum,
            num_train_epochs=args.epochs,
            learning_rate=args.lr,
            warmup_steps=10,
            optim="adamw_8bit",
            bf16=True,
            logging_steps=5,
            save_steps=50,
            save_total_limit=2,
            seed=42,
            report_to="none",
        ),
        dataset_text_field="text",
        max_seq_length=args.max_seq_len,
        packing=True,
    )

    stats = trainer.train()
    print(f"\nTraining complete!")
    print(f"  Total steps: {stats.global_step}")
    print(f"  Final loss: {stats.training_loss:.4f}")

    # Save LoRA adapter
    print(f"Saving adapter to {args.adapter_dir}")
    model.save_pretrained(args.adapter_dir)
    tokenizer.save_pretrained(args.adapter_dir)

    # Export to GGUF if requested
    if args.export_gguf:
        print(f"Exporting to GGUF ({args.gguf_quant}) -> {args.gguf_dir}")
        model.save_pretrained_gguf(
            args.gguf_dir,
            tokenizer,
            quantization_method=args.gguf_quant,
        )
        print("GGUF export complete!")

    print("\nDone! Next steps:")
    print(f"  1. Test adapter: python scripts/finetune.py --export-gguf")
    print(f"  2. Run with vLLM: vllm serve {args.gguf_dir}")


if __name__ == "__main__":
    main()
