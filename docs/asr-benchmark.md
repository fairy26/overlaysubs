# M2 実機 ASR ベンチマーク結果と既定モデルの決定

Apple M2（統合メモリ 16 GiB）実機で、音声認識（ASR）の 2 実装を比較計測した結果をまとめる。

## 計測環境と対象実装

| 項目 | 内容 |
| --- | --- |
| チップ | Apple M2（`sysctl -n machdep.cpu.brand_string`） |
| 統合メモリ | 17,179,869,184 バイト / 16 GiB（`sysctl -n hw.memsize`） |
| OS / ツールチェーン | macOS 27.2.1 (`arm64`), Xcode 27.2 (Swift 6.4.2), Go 1.27 (`CGO_ENABLED=0`) |
| 実装 A（Parakeet） | Parakeet TDT 0.6B v3（`FluidAudio` v0.18.4、`FluidInference/parakeet-tdt-0.6b-v3-coreml`、Core ML `.cpuAndNeuralEngine`） |
| 実装 B（Whisper） | whisper large-v3-turbo（Homebrew `whisper.cpp` 1.9.5_2 `whisper-server`、Metal GPU、`-bo 1 -mc 0 -sns -fa --vad`） |
| 翻訳（同時稼働） | PLaMo 3 Translate 8B Q4_K_M（Homebrew `llama.cpp` 1.0.0 `llama-server`、Metal GPU、`-c 4096 -b 4096 -ngl 999 -t 8 -np 1 --special`） |
| VAD / エンドポインタ | Silero VAD v6.2.0（`ggml-silero-v6.2.0.bin`、threshold=0.5、ヒステリシス 0.35、無音区切り 600 ms、最大発話 15,000 ms、プリロール 200 ms） |
| リソース計測 | プロセス RSS は `ps`（250 ms 間隔）、GPU / ANE 使用率・電力・GPU メモリは `macmon pipe -i 250`（Apple Silicon IOReport 経由）および `ioreg -c IOAccelerator` で計測 |

## 評価用データセット（YouTube 英語動画 3 本）

性質の異なる 3 本の英語 YouTube 動画（計 1,011.7 秒、約 16.9 分）を選び、`capture-helper`（`--save-wav`）で保存した同一の 16 kHz モノラル WAV に対して実装 A・B の両方を実行した。正解テキストはいずれも YouTube の手動英語字幕（`en` VTT）を用いた。

| # | 種別 | 動画 | 時間 | 発話数 (VAD) | 正解語数 | 正解ソース |
| --- | --- | --- | ---: | ---: | ---: | --- |
| 1 | 1 人で話す解説 | TED: Grit: The Power of Passion and Perseverance - Angela Lee Duckworth (`H14bBuluwB8`) | 372.9 s (6m13s) | 50 | 881 | YouTube 手動字幕 (`en`) |
| 2 | 複数人の会話 | BBC Learning English: Naps: Good for your brain? BBC News Review (`Tefu_NvcC0k`) | 355.2 s (5m55s) | 30 (B: 29) | 785 | YouTube 手動字幕 (`en`) |
| 3 | BGM ありの動画 | Kurzgesagt: Nuclear Energy Explained: How does it work? 1/3 (`rcOFV4y5z8c`) | 283.6 s (4m44s) | 19 | 751 | YouTube 手動字幕 (`en`) |

WER の計算前には、大文字小文字の統一、記号・句読点および舞台表記（`[Intro]`、`(Applause)` 等）の除去、一般的な英語短縮形（`don't` → `do not` 等）の展開、および英語数詞と数字表記の正規化を正解・推定の双方に同一ルールで適用した。

動画 3 は BGM が連続しているため Silero VAD が無音区切り（600 ms）になりにくく、最大発話長 15,000 ms で強制カットされる区間が多かった。そのため発話数は 19 と少なく、1 発話あたりの語数が長い分、PLaMo 側の翻訳時間が長くなっている。

## Neural Engine（ANE）動作検証（単体実行）

PLaMo（`llama-server`）を停止した状態で、動画 1（372.9 秒）に対し実装 A と実装 B をそれぞれ単体実行し、Parakeet が CPU や GPU へフォールバックせず Apple Neural Engine 上で推論していることを確認した。

| 項目 | A: Parakeet TDT v3（単体） | B: Whisper large-v3-turbo（単体） |
| --- | ---: | ---: |
| Core ML 設定（実ログ） | `encoder/decoder/joint = cpuAndNeuralEngine`, `preprocessor = cpuOnly` | Metal GPU（`whisper-server`） |
| ANE 電力（平均 / p95 / 最大） | 2,446.3 mW / 3,437.3 mW / 4,045.5 mW | 0.0 mW / 0.0 mW / 0.0 mW |
| ANE アクティブサンプル比率（>= 50 mW） | 26 / 26（100%） | 0 / 308（0%） |
| GPU HW アクティブ率（平均 / 最大） | 1.6% / 3.2%（`ioreg` 平均 0.0%） | 93.0% / 100.0%（`ioreg` 平均 91.4%） |
| GPU 電力（平均 / 最大） | 6.4 mW / 13.2 mW | 8,895.8 mW / 10,731.9 mW |
| ASR 処理時間 / RTF | 6.54 s / 0.0175（実時間の 57 倍速） | 77.06 s / 0.2066（実時間の 4.8 倍速） |
| 純 ASR 遅延（中央値 / p95） | 56.0 ms / 155.6 ms | 1,443.5 ms / 1,703.7 ms |
| ピーク RSS（ASR プロセス計） | 210.1 MB | 1,995.7 MB |
| GPU メモリ使用最大（`IOAccelerator`） | 311.4 MB（デスクトップ基準値） | 2,105.1 MB |

Parakeet は推論中すべてのサンプルで ANE 電力 2.4〜4.0 W を消費する一方、GPU 使用率は 1.6%（6.4 mW、OS デスクトップ描画レベル）に留まっており、GPU や CPU のみへのフォールバックなく Apple Neural Engine 上で動作していることが確認できた。

## M2 実機比較結果（PLaMo 3 Translate 8B 同時稼働）

PLaMo 3 Translate 8B（Q4_K_M、`llama-server`）を同時に稼働させ、`capture-helper` が出力した発話イベントを即座に並列翻訳パイプラインへ流した状態で計測した。

「発話終了→英文」の遅延は、VAD が無音 600 ms（または 15 秒上限）で発話確定するまでの待ち時間（`vad_delay_ms`）と ASR 推論時間（`asr_ms`）の合計であり、参考値として純 ASR 推論時間（`asr_ms` 単体）も併記している。「発話終了→訳文」は上記に PLaMo 3 Translate 8B の翻訳完了時間（および先頭トークン到達までの TTFT）を加えた値である。

### 精度（正規化 WER）とリアルタイム性

| 動画 | 実装 | WER | S / D / I（正解語数） | ASR RTF（合計処理時間） | 翻訳込み RTF（合計処理時間） | リアルタイム可否 |
| --- | --- | ---: | --- | ---: | ---: | --- |
| 1. 1 人解説 (`H14bBuluwB8`) | A: Parakeet | 0.68% | 3 / 0 / 3 (881) | 0.0201 (7.50 s) | 0.3772 (140.66 s) | 可 |
| 1. 1 人解説 (`H14bBuluwB8`) | B: Whisper | 1.59% | 2 / 9 / 3 (881) | 0.4102 (152.98 s) | 0.5811 (216.71 s) | 可 |
| 2. 複数人会話 (`Tefu_NvcC0k`) | A: Parakeet | 2.80% | 12 / 5 / 5 (785) | 0.0212 (7.52 s) | 0.2967 (105.41 s) | 可 |
| 2. 複数人会話 (`Tefu_NvcC0k`) | B: Whisper | 2.17% | 6 / 7 / 4 (785) | 0.2818 (100.09 s) | 0.4555 (161.80 s) | 可 |
| 3. BGM あり (`rcOFV4y5z8c`) | A: Parakeet | 1.46% | 3 / 1 / 7 (751) | 0.0220 (6.24 s) | 0.3718 (105.43 s) | 可 |
| 3. BGM あり (`rcOFV4y5z8c`) | B: Whisper | 2.66% | 4 / 6 / 10 (751) | 0.2509 (71.16 s) | 0.5367 (152.18 s) | 可 |
| 全 3 動画合計（マイクロ平均） | A: Parakeet | 1.61% | 18 / 6 / 15 (2,417) | 0.0210 (21.26 s / 1,011.7 s) | 0.3474 (351.50 s) | 可 |
| 全 3 動画合計（マイクロ平均） | B: Whisper | 2.11% | 12 / 22 / 17 (2,417) | 0.3205 (324.23 s / 1,011.7 s) | 0.5245 (530.69 s) | 可 |

両実装とも 3 動画すべてでリアルタイム（RTF < 1.0）に間に合った。

### 遅延（中央値 / p95、PLaMo 8B 同時稼働）

| 動画 | 実装 | 純 ASR 推論（中央値 / p95） | 発話終了→英文（中央値 / p95） | PLaMo 8B 翻訳のみ（中央値 / p95） | 発話終了→訳文開始 TTFT（中央値 / p95） | 発話終了→訳文完了（中央値 / p95） |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| 1. 1 人解説 | A: Parakeet | 69 ms / 197 ms | 670 ms / 707 ms | 2,518 ms / 4,437 ms | 2,296 ms / 2,792 ms | 3,191 ms / 4,769 ms |
| 1. 1 人解説 | B: Whisper | 2,892 ms / 3,686 ms | 3,473 ms / 4,158 ms | 4,174 ms / 8,009 ms | 5,839 ms / 7,359 ms | 7,669 ms / 11,814 ms |
| 2. 複数人会話 | A: Parakeet | 110 ms / 204 ms | 654 ms / 719 ms | 3,864 ms / 4,945 ms | 2,518 ms / 3,054 ms | 4,261 ms / 5,126 ms |
| 2. 複数人会話 | B: Whisper | 3,406 ms / 4,279 ms | 3,526 ms / 4,542 ms | 5,236 ms / 8,903 ms | 6,506 ms / 8,463 ms | 9,151 ms / 13,172 ms |
| 3. BGM あり | A: Parakeet | 204 ms / 273 ms | 209 ms / 718 ms | 5,956 ms / 6,632 ms | 3,312 ms / 3,847 ms | 6,165 ms / 6,899 ms |
| 3. BGM あり | B: Whisper | 3,690 ms / 3,902 ms | 3,729 ms / 3,974 ms | 7,420 ms / 12,428 ms | 7,416 ms / 10,188 ms | 11,160 ms / 16,303 ms |
| 全 3 動画統合 | A: Parakeet | 91 ms / 222 ms | 664 ms / 713 ms | — | — | — |
| 全 3 動画統合 | B: Whisper | 3,110 ms / 3,958 ms | 3,554 ms / 4,350 ms | — | — | — |

### ピークメモリ・GPU 使用率・ANE 電力（PLaMo 8B 同時稼働）

| 動画 | 実装 | ピーク総 RSS（内 ASR 分） | GPU メモリ使用最大 | GPU HW 使用率（平均 / p95） | GPU 電力（平均） | ANE 電力（最大） |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| 1. 1 人解説 | A: Parakeet | 2,743 MB (173 MB) | 5,866 MB | 98.9% / 100.0% | 12.18 W | 3,148 mW |
| 1. 1 人解説 | B: Whisper | 4,283 MB (1,683 MB) | 7,442 MB | 99.1% / 100.0% | 11.86 W | 14 mW |
| 2. 複数人会話 | A: Parakeet | 2,758 MB (177 MB) | 5,456 MB | 98.7% / 100.0% | 12.57 W | 2,703 mW |
| 2. 複数人会話 | B: Whisper | 4,302 MB (1,734 MB) | 7,434 MB | 99.7% / 100.0% | 10.19 W | 0 mW |
| 3. BGM あり | A: Parakeet | 2,812 MB (189 MB) | 5,456 MB | 99.3% / 100.0% | 12.92 W | 3,223 mW |
| 3. BGM あり | B: Whisper | 4,316 MB (1,746 MB) | 7,458 MB | 99.5% / 100.0% | 9.69 W | 0 mW |

PLaMo 8B を連続稼働させた状態ではどちらの実装でも Metal GPU がほぼ 100% 稼働するが、GPU の競合有無が遅延に決定的な差を生んだ。Whisper は単体では動画 1 の純 ASR 中央値が 1,444 ms だったのに対し、PLaMo 8B と同時稼働すると Metal GPU を取り合うため 2,892 ms（約 2 倍）に悪化し、さらに PLaMo 側の翻訳中央値も 2,518 ms → 4,174 ms（約 1.7 倍）へ押し下げられた。一方 Parakeet は推論を ANE に逃がすため、PLaMo 8B と同時稼働しても純 ASR 中央値 69 ms を維持し、PLaMo 8B に GPU 帯域を丸ごと譲ることができた。

## 既定モデルの決定と判断理由

16 GB 以上の Apple Silicon Mac（および 8 GB Mac 共通）の既定 ASR エンジンは、実装 A の Parakeet TDT v3（`parakeet`）とする。

判断の根拠は以下の 4 点である。

1. WER の目安（A が B より 2 ポイント以上悪くないこと）を大きく上回る精度だった。全 3 動画統合のマイクロ平均 WER は A が 1.61%、B が 2.11% であり、A の方が 0.50 ポイント良好だった。動画別でも A は 1 人解説（0.68% vs 1.59%）と BGM あり（1.46% vs 2.66%）で B を上回り、複数人会話のみ B が優位（2.80% vs 2.17%、差 +0.63 ポイント）だったがいずれも基準の 2 ポイント以内である。
2. 翻訳まで含めた遅延（p95）が 2.4 倍前後短縮された。PLaMo 8B 同時稼働時の発話終了から訳文完了までの p95 遅延は、A が 4.77〜6.90 秒（先頭トークン TTFT は 2.79〜3.85 秒）であったのに対し、B は 11.81〜16.30 秒（TTFT は 7.36〜10.19 秒）に達した。リアルタイム字幕用途では 10 秒超の遅延は視聴体験を大きく損なう。
3. Apple Neural Engine との負荷分散が機能している。単体検証で ANE 電力 2.4〜4.0 W・GPU 使用率 1.6% を確認しており、PLaMo 8B の Metal GPU 推論と競合しない。
4. メモリ消費が大幅に少ない。PLaMo 8B 同時稼働時のピーク総 RSS は A が約 2.8 GB（GPU メモリ使用最大約 5.5〜5.9 GB）、B が約 4.3 GB（同約 7.4 GB）であり、16 GB 統合メモリ環境でブラウザや OS に残る余裕が約 1.5〜2.0 GB 増える。

実装 B（`whisper` / whisper large-v3-turbo）も `ASREngine` 実装として残しており、設定ファイル（`settings.json` の `asr_engine`）、設定画面の UI、および CLI フラグ（`--asr whisper`）からいつでも切り替えられる。
