# OverlaySubs (macOS / Go + MyGo)

macOS（Apple Silicon）向けの、YouTube などの英語音声をすべてローカル処理でリアルタイム日本語字幕化するオーバーレイアプリ。

Tauri や webview を使わず、Pure Go（`CGO_ENABLED=0`）の [MyGo](https://github.com/egoist/mygo) ネイティブ UI（GPU 描画）と、ScreenCaptureKit・Silero VAD・差し替え可能な ASR を担う Swift 補助プログラム（`helper/`）で構成している。

既存の Rust / Tauri 実装（`crates/core/`, `src-tauri/`, `ui/`）は参照用として残しているが、新しいアプリからは一切使用しない。既存実装から読み取った仕様と設計の詳細は [docs/rewrite-notes.md](docs/rewrite-notes.md) を参照。

## アーキテクチャ

```text
[Swift 補助プログラム helper/ (capture-helper)]
  ScreenCaptureKit で Chrome 等の対象アプリ音声を取得（または --input-wav）
    → 16 kHz モノラル Float32（--save-wav で保存可）
    → Silero VAD (ggml-silero-v6.2.0.bin) で発話ごとに区切る
    → ASREngine（parakeet: FluidAudio / Core ML / ANE [既定] / whisper: whisper-server HTTP [Metal]）
    → stdout に JSON Lines 出力（ログは stderr）
        ↓
[Go 本体 cmd/overlaysubs (MyGo ネイティブ UI、CGO_ENABLED=0)]
  サブプロセス管理（whisper-server は whisper 選択時のみ起動、llama-server、capture-helper）
    → PFML3 プロンプト構築（直近 3 発話の文脈・用語集・敬体/常体）
    → llama-server（PLaMo 3 Translate 8B / 2B Q4_K_M、Metal GPU）
    → 画面下部中央の透過・枠なし・最前面・クリックスルー字幕ウィンドウに表示
```

## 必要環境

- macOS 14 以降（Apple Silicon）
- Go 1.27 以降
- Xcode Command Line Tools（Swift 6）
- Homebrew でインストールする推論サーバ・VAD ライブラリ

```bash
brew install llama.cpp whisper.cpp
```

## ビルドと実行

```bash
make build
```

`bin/overlaysubs`（Pure Go / `CGO_ENABLED=0`）と `bin/capture-helper`（Swift リリースビルド）が生成される。

初回は必要なモデル（`models.json` に定義された URL・SHA-256 検証・レジューム対応）を GUI の「Download Models」ボタンまたは CLI でダウンロードする。

```bash
# 既定モデルをダウンロード（16GB 以上は PLaMo 3 8B Q4_K_M、8GB は 2B Q4_K_M）
./bin/overlaysubs --download-only

# 2B モデルを指定してダウンロード
./bin/overlaysubs --download-only --translator 2b

# GUI アプリを起動
./bin/overlaysubs
```

初回キャプチャ時には macOS の「画面収録とシステムオーディオ録音」権限が求められる。許可後にアプリを再起動すると音声取得が有効になる。

## CLI オプション

| フラグ | 説明 |
| --- | --- |
| `--asr <whisper\|parakeet>` | 使用する ASR エンジンを指定する |
| `--translator <8b\|2b\|model-id>` | 翻訳モデル（PLaMo 3 Translate 8B / 2B Q4_K_M）を切り替える |
| `--app <bundle-id\|all>` | 音声取得対象アプリの Bundle ID（既定 `com.google.Chrome`、`all` でシステム全体） |
| `--form <polite\|plain>` | 日本語訳の文体（敬体 / 常体） |
| `--topic <text>` | 翻訳の文脈ヒント |
| `--save-wav <path>` | 取得した 16kHz モノラル音声を WAV に保存する |
| `--input-wav <path>` | WAV ファイルを再生入力として VAD → ASR → 翻訳を実行する |
| `--headless` | GUI を開かず標準出力に翻訳結果を表示する |
| `--download-only` | 必要なモデルをダウンロードして終了する |

## テスト

```bash
make test
```

Go のユニットテスト（`CGO_ENABLED=0 go test ./...`、PFML3・モデルレジストリ・ダウンロード・セッション履歴・`llama-server` クライアント・MyGo 字幕オーバーレイのヘッドレス描画テスト）と、Swift 補助プログラムの `swift test` をまとめて実行する。

## ライセンス

本体コードは MIT ライセンス（[LICENSE](LICENSE)）。翻訳モデルの PLaMo 3 Translate には Preferred Networks の PLaMo Community License が適用される。Built with PLaMo.
