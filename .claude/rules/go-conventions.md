---
description: Go コーディング規約 (実証済みルールのみ)
---

## エラー処理

- `fmt.Errorf("doing X: %w", err)` でラップして伝播
- `errors.Is` / `errors.As` で判定。文字列マッチング禁止
- log-and-return 禁止: ログするか return するか、どちらか一方

## Interface

- consumer 側で定義 (Go implicit interface)
- 1-3 メソッドに絞る
- provider に大きい interface を置かない

## 禁止パターン

- `init()` 関数
- グローバル可変状態
- `panic()` で期待されるエラーを処理
- `_` でエラーを握りつぶす

## Context

- 最初の引数に `ctx context.Context`
- Context に設定や依存を詰めない

## ファイル

- 1ファイル 500行以下。超えたら分割
- 未使用コードは即削除
