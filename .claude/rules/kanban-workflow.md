## 看板駆動改善ワークフロー

GitHub Projects (haryoiro/projects/2) で改善タスクを管理する。

### カラム定義

| Status | 意味 |
|--------|------|
| Backlog | アイデア・未優先 |
| Todo | 優先済み、作業可能 |
| In Progress | Claude Code が作業中 |
| Review | PR 作成済み、オーナーレビュー待ち |
| Done | マージ完了 |

### ワークフロー

1. **タスク取得**: `gh project item-list 2 --owner haryoiro` で Todo のアイテムを確認
2. **着手**: アイテムの Status を In Progress に変更
3. **作業**: git worktree で作業。ブランチ名は `improve/<issue番号>-<slug>`
4. **PR 作成**: `gh pr create` でPR作成。本文に issue リンク (`Closes #N`) を含める
5. **レビュー依頼**: アイテムの Status を Review に変更
6. **完了**: オーナーがマージ後 Done に移動

### Project IDs (GraphQL 操作用)

- Project ID: `PVT_kwHOA0wAzs4BTsU3`
- Project number: `2`
- Status field ID: `PVTSSF_lAHOA0wAzs4BTsU3zhA5krk`
- Option IDs: Backlog=`81ffcfd9` Todo=`f75ad846` InProgress=`47fc9ee4` Review=`90158f56` Done=`98236657`

### Status 変更コマンド

```bash
gh project item-edit --project-id PVT_kwHOA0wAzs4BTsU3 \
  --id <ITEM_ID> \
  --field-id PVTSSF_lAHOA0wAzs4BTsU3zhA5krk \
  --single-select-option-id <OPTION_ID>
```

### 制約

- 同時に In Progress にできるのは 1 タスクまで
- PR は必ず worktree で作成 (本体の hot-reload を回避)
- マージはオーナーのみ。Claude Code は絶対にマージしない
