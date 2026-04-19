#!/usr/bin/env node
// tsp compile 後に走る post-process スクリプト。
//
// TypeSpec の @extension は operation 直下にしか x-* キーを配置できないが、
// ogen の x-ogen-raw-response: true は media-type オブジェクトで読まれる。
// ここで operation 直下の x-ogen-raw-response を responses/*/content/<mt>/
// にリフトダウンする。
//
// 使い方:
//   node inject-raw-response.js <openapi.yaml ...>

const fs = require("fs");
const path = require("path");

function processYaml(filePath) {
  const lines = fs.readFileSync(filePath, "utf8").split("\n");
  const output = [];

  // ブロック単位のパス解析のため現在の operation 区間を追跡する。
  // YAML なので indent ベースで範囲を判定。
  let opBlockStart = -1; // lines index where operation (GET/POST/PUT/DELETE/PATCH) starts
  let opIndent = -1;
  const toInject = []; // {opStart, opIndent}

  // 1st pass: x-ogen-raw-response: true を含む operation を探し、
  //           その行を削除して後で media-type に追加する位置を記録する。
  const keep = [];
  let skipRaw = new Set();
  for (let i = 0; i < lines.length; i++) {
    const ln = lines[i];
    const m = ln.match(/^(\s*)x-ogen-raw-response:\s*true\s*$/);
    if (m) {
      skipRaw.add(i);
    }
  }

  // operation block を、"${indent}${METHOD}:" で開始、同 indent の次のキーで終了と定義。
  // その範囲内で skipRaw があれば、raw フラグ立て → responses/*/content/<mt>/ に追加
  // 実装: 行を走査して現在の operation ブロックを決定。
  const isMethod = (s) => /^(get|post|put|delete|patch|options|head|trace):\s*$/.test(s);
  let opRange = null; // { from, to, hasRaw }

  const operations = []; // collected { from, to, hasRaw, methodIndent }

  for (let i = 0; i < lines.length; i++) {
    const raw = lines[i];
    const trimmed = raw.replace(/\r?$/, "");
    const stripped = trimmed.replace(/^\s*/, "");
    const indent = trimmed.length - stripped.length;
    const isMethodLine = /^[a-z]+:\s*$/.test(stripped) && isMethod(stripped);

    if (isMethodLine) {
      if (opRange) {
        opRange.to = i - 1;
        operations.push(opRange);
      }
      opRange = { from: i, to: -1, indent, hasRaw: false };
      continue;
    }
    if (opRange && opRange.indent >= 0) {
      // 次の method line または indent <= opRange.indent の同レベルキーで終了。
      if (stripped === "" ) {
        // 空行は範囲内扱い
      } else if (indent <= opRange.indent && !isMethodLine) {
        opRange.to = i - 1;
        operations.push(opRange);
        opRange = null;
        // この行が method なら再処理したいが、上の分岐で既に捕捉済み。
        // method 以外なら単に抜けるだけで OK。
      } else if (skipRaw.has(i)) {
        opRange.hasRaw = true;
      }
    }
  }
  if (opRange) {
    opRange.to = lines.length - 1;
    operations.push(opRange);
  }

  // 2nd pass: 出力を作る。
  //   - skipRaw の行はスキップ (raw フラグだけ覚えておく)
  //   - hasRaw=true の operation 内で、"${contentIndent + 2}<media-type>:" 行を見つけたら
  //     直後に "${mtIndent + 2}x-ogen-raw-response: true" を注入する。
  // 実装: operation 開始位置 -> 終了位置までの行集合を見て、
  // responses -> <status> -> content -> <media-type> を indent で認識、
  // 最初の media-type だけに付与する (複数 media-type 返す API はここでは扱わない)。

  // まず operations を map[from->op] にして探しやすくする。
  const opsByFrom = new Map();
  for (const op of operations) {
    opsByFrom.set(op.from, op);
  }

  let currentOp = null;
  // 状態機械: within operation, track nesting by indent.
  let contentMt = null; // { indent, mediaTypeLine } — the first media-type line we need to inject after
  let injectedForOp = false;

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (opsByFrom.has(i)) {
      currentOp = opsByFrom.get(i);
      injectedForOp = false;
    }
    if (currentOp && i > currentOp.to) {
      currentOp = null;
      injectedForOp = false;
    }

    if (skipRaw.has(i)) {
      // drop operation-level marker
      continue;
    }

    output.push(line);

    if (!currentOp || !currentOp.hasRaw || injectedForOp) continue;

    // detect "<indent>content:" then next non-empty child is a media-type key.
    // もっと単純に: line that matches /^(\s*)(application|text|image|audio|video|multipart)\/[^ :]+:\s*$/
    const mt = line.match(/^(\s+)([a-z]+\/[A-Za-z0-9.+\-*]+):\s*$/);
    if (mt) {
      const childIndent = mt[1] + "  ";
      output.push(`${childIndent}x-ogen-raw-response: true`);
      injectedForOp = true;
    }
  }

  fs.writeFileSync(filePath, output.join("\n"));
}

const files = process.argv.slice(2);
if (files.length === 0) {
  console.error("usage: inject-raw-response.js <openapi.yaml ...>");
  process.exit(1);
}
for (const f of files) {
  processYaml(path.resolve(f));
  console.log(`processed ${f}`);
}
