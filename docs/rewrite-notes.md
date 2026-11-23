# OverlaySubs Go + MyGo 書き直し設計メモ

既存の Rust / Tauri 実装（`crates/core`, `src-tauri`, `models.json`）から読み取った仕様と、macOS（Apple Silicon）向けの新アーキテクチャをまとめる。

## 実行環境の確認結果

対象マシンで `sysctl` を実行して確認したハードウェア仕様は以下のとおり。

| 項目 | 計測コマンド | 実測値 |
| --- | --- | --- |
| チップ | `sysctl -n machdep.cpu.brand_string` | `Apple M2` |
| 統合メモリ | `sysctl -n hw.memsize` | `17179869184` バイト（16 GiB） |
| 論理コア数 | `sysctl -n hw.ncpu` | `8` |
| OS / アーキテクチャ | `sw_vers` / `uname -m` | macOS 27.2.1 (`arm64`) |

## 1. PLaMo 3 Translate のプロンプト形式と生成パラメータ

参照元は `crates/core/src/pfml.rs`、`crates/core/src/session.rs`、`src-tauri/src/engine.rs`。

### PFML3 プロンプト形式

タグは次の形式で連結する。

```text
<|plamo:tag|>{name}[<|plamo:key|>{k}<|plamo:val|>{v}...]<|plamo:msg|>{msg}
```

プロンプト全体の構成順序は固定されている。

1. データセット指定として `<|plamo:tag|>dataset<|plamo:msg|>translation` を先頭に置く。
2. トピック（`context`）が空でない場合のみ `<|plamo:tag|>context<|plamo:msg|>{context}` を追加する。
3. 過去の翻訳履歴（`history`、直近最大 3 発話、`HISTORY = 3`）を古い順に `input` / `output` ペアとして並べる。
4. 用語集（`glossary`）のうち、現在の入力テキストに含まれる原語だけを抽出し、`<|plamo:tag|>glossary<|plamo:msg|><|plamo:index|>{src}<|plamo:item|>{dst}...` として追加する。
5. 翻訳対象テキストを `<|plamo:tag|>input<|plamo:key|>lang<|plamo:val|>{source_lang}<|plamo:msg|>{text}` として追加する（`source_lang` 未指定時は `lang` 属性を省略する）。
6. 出力開始タグ `<|plamo:tag|>output<|plamo:key|>lang<|plamo:val|>{target_lang}[<|plamo:key|>form<|plamo:val|>{plain|polite}]<|plamo:msg|>` で終端する。

`form` 属性（敬体 `polite` / 常体 `plain`）は `target_lang` が `ja` のときにのみ付与する。

### 生成パラメータと停止条件

| パラメータ | 既存実装の値 | `llama-server` での対応 |
| --- | --- | --- |
| コンテキスト長 (`n_ctx`) | `4096` | 起動引数 `-c 4096` |
| バッチサイズ (`n_batch`) | `4096` | 起動引数 `-b 4096` |
| 最大生成トークン数 (`MAX_NEW`) | `256` | `/completion` の `n_predict: 256` |
| サンプリング | `LlamaSampler::greedy()` | `temperature: 0.0`, `top_k: 1`, `repeat_penalty: 1.0` |
| 停止文字列 (`pfml::STOP`) | `["<|plamo:tag|>", "<|plamo:bos|>"]` および EOG | `stop: ["<|plamo:tag|>", "<|plamo:bos|>"]` |
| 特殊トークン処理 | `tokenize(..., add_bos=true, parse_special=true)` と `token_to_piece(tok, special=true)` | `llama-server` に `--special` を付与して特殊トークン出力を有効にし、停止文字列で確実に終了させる |
| GPU オフロード (`n_gpu_layers`) | `999`（Metal 全層） | 起動引数 `-ngl 999` |
| スレッド数 | `min(cpu_threads, 8)` | 起動引数 `-t <min(ncpu, 8)>` |

## 2. llama.cpp と whisper.cpp のバージョンおよびパッチ有無

参照元は `src-tauri/Cargo.toml`、`Cargo.lock`。

### llama.cpp

既存実装は `llama-cpp-2` 0.1.162（`utilityai/llama-cpp-rs` のコミット `0364f5da1c6a6ee37f507031f116ce4a09b28594`）を固定している。コメントには crates.io の 0.1.161 が `plamo3` トークナイザー導入前であるため git rev を指定した旨が書かれている。

このリビジョンが参照するサブモジュール `llama-cpp-sys-2/llama.cpp` は上流 `ggml-org/llama.cpp` のコミット `8642541b255bfb5dc39cd351a178be09013a55d6`（`llama.cpp : bump version to 0.8.0 (#31309)`、2026-10-28）であり、`build.rs` による独自パッチ適用はない。PLaMo 3 モデルと `plamo3` トークナイザーは上流 `llama.cpp` にマージ済みであるため、カスタムパッチは不要である。

新実装では Homebrew の `llama.cpp` v1.0.0（rev `b1b770d90d94cad9e9ea5b993bdbc383a522fb28`、Metal 有効、`0.8.0` 以降）に含まれる `llama-server` をそのまま利用できる。

### whisper.cpp

既存実装は `whisper-rs` 0.16.0（`whisper-rs-sys` 0.15.0）を使用している。音声認識時には greedy (`best_of: 1`)、`no_context: true`、`single_segment: true`、`suppress_blank: true`、`suppress_nst: true`、および直前発話（最大 150 文字）を `initial_prompt` として渡している。

Homebrew の `whisper.cpp` v1.9.5_2 ボトル（`/opt/homebrew/bin/whisper-server`, `/opt/homebrew/include/whisper.h`, `/opt/homebrew/lib/libwhisper.dylib`）には `whisper-server` も同梱されている（Formula の `-DWHISPER_BUILD_EXAMPLES=ON` によりビルド済み、Metal 有効）。したがって、追加ビルドなしで Homebrew の `whisper-server` をそのままサブプロセスとして利用できる。Swift 補助プログラムの Silero VAD でも、同じ Homebrew の `libwhisper`（`whisper.h` の `whisper_vad_*` C API）をリンクして `ggml-silero-v6.2.0.bin` をそのまま読み込む。

## 3. VAD とエンドポインタの設定値

参照元は `crates/core/src/endpoint.rs`、`src-tauri/src/engine.rs`、`crates/core/src/session.rs`。

| パラメータ | 値 | 役割 |
| --- | --- | --- |
| VAD モデル | `silero-vad-v6.2.0` (`ggml-silero-v6.2.0.bin`) | 発話区間の確率推定 |
| サンプリングレート (`RATE`) | `16,000` Hz（モノラル Float32） | 音声認識・VAD 共通フォーマット |
| フレーム長 (`FRAME`) | `512` サンプル（32 ms） | Silero VAD の 1 ウィンドウ |
| 推論バッチ (`BATCH`) | `4` フレーム（128 ms） | 推論呼び出し間隔 |
| 開始しきい値 (`threshold`) | `0.5` | 発話開始の判定確率 |
| 継続しきい値（ヒステリシス） | `0.35` (`threshold - 0.15`) | 発話中の継続判定確率 |
| 無音区切り時間 (`silence_ms`) | `600` ms | 無音が続いたら発話を確定し末尾無音をトリム |
| 最大発話長 (`max_ms`) | `15,000` ms | 長すぎる発話の強制カット |
| プリロール (`pre_roll_ms`) | `200` ms | 発話開始直前の音声保持（頭切れ防止） |
| Silero 履歴コンテキスト (`CONTEXT`) | `16 * 512 = 8,192` サンプル（約 0.512 秒） | LSTM 状態リセットへのウォームアップ |

後処理として `crates/core/src/session.rs` と同様に、無音・音楽時の Whisper ハルシネーション除外（`"thank you for watching"`, `"thanks for watching"`, `"please subscribe"`, `"subtitles by"` 等、英数字 2 文字未満の除外）、末尾句点補完（`punctuate`）、および 10 文字以上を単位とする文分割（`split_sentences`）を Go 側の翻訳パイプラインに組み込む。

## 4. モデルのダウンロード元（`models.json`）

既存の `models.json` に定義されている URL・ファイル名・サイズ・SHA-256 をそのまま利用する。本実装で主に使用するモデルは以下のとおり。

| ID | 種別 | ファイル名 | サイズ（バイト） | SHA-256 | ダウンロード URL |
| --- | --- | --- | ---: | --- | --- |
| `whisper-large-v3-turbo` | ASR | `ggml-large-v3-turbo.bin` | `1624555275` | `1fc70f774d38eb169993ac391eea357ef47c88757ef72ee5943879b7e8e2bc69` | `https://huggingface.co/ggerganov/whisper.cpp/resolve/5359861c739e955e79d9a303bcbc70fb988958b1/ggml-large-v3-turbo.bin` |
| `whisper-large-v3-turbo-q5_0` | ASR | `ggml-large-v3-turbo-q5_0.bin` | `574041195` | `394221709cd5ad1f40c46e6031ca61bce88931e6e088c188294c6d5a55ffa7e2` | `https://huggingface.co/ggerganov/whisper.cpp/resolve/5359861c739e955e79d9a303bcbc70fb988958b1/ggml-large-v3-turbo-q5_0.bin` |
| `plamo-3-translate-8b-q4_k_m` | Translator | `plamo-3-translate-8b-Q4_K_M.gguf` | `5280442080` | `47a04e86a67bcc352fa186f01159060aae594b73993e1d49dbfc0ef0081d01ad` | `https://huggingface.co/pfnet/plamo-3-translate-8b-GGUF/resolve/2234f168757f570714b333049c9d018161ec5277/plamo-3-translate-8b-Q4_K_M.gguf` |
| `plamo-3-translate-2b-q4_k_m` | Translator | `plamo-3-translate-2b-Q4_K_M.gguf` | `1807199392` | `f36d0813dd95573a852ab5d76e374c543632d9e292929e922bcdbf7e53b494e0` | `https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF/resolve/232fbd7916519edecddfdae0beae7e76fe70453f/plamo-3-translate-2b-Q4_K_M.gguf` |
| `silero-vad-v6.2.0` | VAD | `ggml-silero-v6.2.0.bin` | `885098` | `2aa269b785eeb53a82983a20501ddf7c1d9c48e33ab63a41391ac6c9f7fb6987` | `https://huggingface.co/ggml-org/whisper-vad/resolve/9ffd54a1e1ee413ddf265af9913beaf518d1639b/ggml-silero-v6.2.0.bin` |

ダウンロード実装（`src-tauri/src/download.rs` 相当）は `.part` ファイルへの HTTP `Range` レジューム、完了後の全体 SHA-256 照合、および最終ファイルへのアトミックリネームを踏襲する。

## 5. ハードウェア別モデル選択ルール

既存の `crates/core/src/tier.rs` では Apple Silicon の統合メモリ（`sysctl -n hw.memsize`）を基準に 512 MiB の余裕を持たせてティア分けしていた（32 GB 以上を High、16 GB 以上を Mid、8 GB 以上を Low）。

新しい macOS 専用版では、実機環境（Apple M2 16 GiB 等）および差し替え可能な ASR 設計に合わせて以下のように簡略化した既定選択ルールを採用する。

| Apple Silicon 統合メモリ | 既定の翻訳モデル | 既定の ASR エンジン |
| --- | --- | --- |
| 8 GB クラス（`16 GiB - 512 MiB` 未満） | `plamo-3-translate-2b-q4_k_m`（PLaMo 3 Translate 2B Q4_K_M） | `parakeet` |
| 16 GB 以上（`16 GiB - 512 MiB` 以上） | `plamo-3-translate-8b-q4_k_m`（PLaMo 3 Translate 8B Q4_K_M） | 暫定 `whisper`（ベンチマーク結果で最終決定、設定・CLI で切替可能） |

翻訳モデル（8B / 2B）および ASR エンジン（`whisper` / `parakeet`）はいずれも設定ファイル・UI・CLI フラグから手動で切り替えられる。

## 6. 新アーキテクチャとプロセス間プロトコル

Go 側は `CGO_ENABLED=0` の Pure Go（MyGo）を維持し、ネイティブ音声取得・VAD・ASR を Swift 補助プログラム `helper/` に集約する。

```text
[Swift 補助プログラム helper/ (capture-helper)]
  入力: ScreenCaptureKit (対象アプリ単位フィルタ / システム音声) または --input-wav <path>
        (--save-wav <path> 指定時は 16kHz モノラル WAV に録音)
    ↓ 16kHz モノラル Float32
  Silero VAD (libwhisper + ggml-silero-v6.2.0.bin、発話単位に分割)
    ↓ 発話 Float32 PCM
  ASREngine プロトコル
    - whisper  : ローカル whisper-server HTTP API (/inference) を呼び出す
    - parakeet : Parakeet TDT v3 (FluidAudio / Core ML、後続フェーズで組み込み)
    ↓ stdout: JSON Lines（ログは stderr）

[Go 本体 (MyGo ネイティブ UI、CGO_ENABLED=0)]
  サブプロセス管理:
    - whisper-server (--asr whisper の場合のみ起動)
    - llama-server (PLaMo 3 Translate GGUF、Metal GPU)
    - capture-helper
  翻訳パイプライン:
    - ハルシネーション除去、文区切り、直前3発話の履歴保持
    - PFML3 プロンプト構築 → llama-server /completion (SSE ストリーミング)
  UI (MyGo package ui、GPU 描画):
    - 字幕オーバーレイウィンドウ (Transparent, Frameless, AlwaysOnTop, IgnoreMouseEvents)
      Screen.PrimaryDisplay().WorkArea から画面下部中央に配置
    - コントロール / 設定ウィンドウ
```

### `helper/` → Go の stdout JSON Lines プロトコル

1 行 1 JSON オブジェクトとして stdout に出力し、診断ログはすべて stderr へ出力する。

起動完了通知の例を以下に示す。

```json
{"type":"ready","engine":"whisper"}
```

発話確定通知の例を以下に示す。

```json
{"type":"utterance","id":1,"text":"Hello everyone, welcome back.","start_ms":1200,"end_ms":3400,"asr_ms":180,"engine":"whisper"}
```

エラー通知の例を以下に示す。

```json
{"type":"error","message":"Screen Recording permission denied"}
```

`capture-helper` の主要 CLI オプションは次のとおりとする。

| フラグ | 説明 |
| --- | --- |
| `--asr <whisper\|parakeet>` | 使用する ASR エンジンを選択する（既定 `whisper`） |
| `--whisper-url <url>` | `whisper` エンジン選択時の `whisper-server` ベース URL（例 `http://127.0.0.1:18100`） |
| `--vad-model <path>` | `ggml-silero-v6.2.0.bin` のパス（未指定時はエネルギーベースフォールバック可） |
| `--app <bundle-id>` | ScreenCaptureKit で対象にするアプリの Bundle ID（例 `com.google.Chrome`、空でシステム全体） |
| `--language <code>` | ASR の入力言語（既定 `en`） |
| `--save-wav <path>` | 取得した 16kHz モノラル音声を WAV ファイルへ保存する |
| `--input-wav <path>` | ScreenCaptureKit の代わりに WAV ファイルを読み込み VAD → ASR を実行する |
| `--list-apps` | 起動中のアプリ一覧（Bundle ID と名前）を JSON で出力して終了する |
