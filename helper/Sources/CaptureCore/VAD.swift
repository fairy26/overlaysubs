import CWhisper
import Foundation

public let kVADFrameSamples: Int = 512 // 32 ms at 16 kHz
private let kVADBatchFrames: Int = 4   // 128 ms per VAD call
private let kSileroContextSamples: Int = 16 * kVADFrameSamples // ~0.512 s warmup

public protocol VADModel {
    /// Returns one speech probability per 512-sample frame; `pcm.count` is a multiple of 512.
    func probs(pcm: [Float]) throws -> [Float]
}

public enum VADError: LocalizedError {
    case loadFailed(String)
    case detectFailed

    public var errorDescription: String? {
        switch self {
        case .loadFailed(let path):
            return "failed to load Silero VAD model from \(path)"
        case .detectFailed:
            return "whisper_vad_detect_speech failed"
        }
    }
}

/// Silero VAD backed by whisper.cpp's `whisper_vad_*` C API (`ggml-silero-v6.2.0.bin`).
public final class SileroVAD: VADModel {
    private static let backendInit: Void = {
        whisper_log_set({ level, text, _ in
            guard level == GGML_LOG_LEVEL_ERROR, let text = text else { return }
            let msg = String(cString: text).trimmingCharacters(in: .whitespacesAndNewlines)
            if !msg.isEmpty {
                logStderr("whisper-vad: \(msg)")
            }
        }, nil)
        ggml_backend_load_all()
    }()

    private var ctx: OpaquePointer?

    public init(modelPath: String) throws {
        _ = SileroVAD.backendInit
        var params = whisper_vad_default_context_params()
        params.n_threads = 1
        params.use_gpu = false
        guard let c = modelPath.withCString({ whisper_vad_init_from_file_with_params($0, params) }) else {
            throw VADError.loadFailed(modelPath)
        }
        whisper_vad_reset_state(c)
        self.ctx = c
    }

    deinit {
        if let c = ctx {
            whisper_vad_free(c)
        }
    }

    public func probs(pcm: [Float]) throws -> [Float] {
        guard let c = ctx else { throw VADError.detectFailed }
        let newFrames = pcm.count / kVADFrameSamples
        guard newFrames > 0 else { return [] }

        let ok = pcm.withUnsafeBufferPointer { buf -> Bool in
            guard let base = buf.baseAddress else { return false }
            return whisper_vad_detect_speech_no_reset(c, base, Int32(buf.count))
        }
        guard ok else {
            throw VADError.detectFailed
        }

        let nProbs = Int(whisper_vad_n_probs(c))
        guard let rawPtr = whisper_vad_probs(c), nProbs >= newFrames else {
            throw VADError.detectFailed
        }
        let startIdx = nProbs - newFrames
        var out = [Float](repeating: 0, count: newFrames)
        for i in 0..<newFrames {
            out[i] = rawPtr[startIdx + i]
        }
        return out
    }
}

/// Plain RMS gate for tests or fallback when no Silero model path is given.
public final class EnergyVAD: VADModel {
    public let rmsThreshold: Float

    public init(rmsThreshold: Float = 0.01) {
        self.rmsThreshold = rmsThreshold
    }

    public func probs(pcm: [Float]) throws -> [Float] {
        let frames = pcm.count / kVADFrameSamples
        var out: [Float] = []
        out.reserveCapacity(frames)
        for f in 0..<frames {
            let start = f * kVADFrameSamples
            var sumSq: Float = 0
            for i in start..<(start + kVADFrameSamples) {
                let s = pcm[i]
                sumSq += s * s
            }
            let rms = sqrt(sumSq / Float(kVADFrameSamples))
            out.append(rms >= rmsThreshold ? 1.0 : 0.0)
        }
        return out
    }
}

/// Configuration matching `crates/core/src/endpoint.rs` (`EndpointConfig::default`).
public struct EndpointConfig: Sendable, Equatable {
    public var threshold: Float
    public var silenceMs: Int
    public var maxMs: Int
    public var preRollMs: Int

    public init(
        threshold: Float = 0.5,
        silenceMs: Int = 600,
        maxMs: Int = 15_000,
        preRollMs: Int = 200
    ) {
        self.threshold = threshold
        self.silenceMs = silenceMs
        self.maxMs = maxMs
        self.preRollMs = preRollMs
    }
}

public struct UtteranceSegment: Sendable, Equatable {
    public let samples: [Float]
    public let startMs: Int
    public let endMs: Int
    public let vadDelayMs: Int

    public init(samples: [Float], startMs: Int, endMs: Int, vadDelayMs: Int = 0) {
        self.samples = samples
        self.startMs = startMs
        self.endMs = endMs
        self.vadDelayMs = vadDelayMs
    }
}

/// Cuts 16 kHz mono Float32 audio into utterances using per-frame speech probabilities
/// with hysteresis (`threshold - 0.15`), pre-roll, silence trimming, and max utterance cap.
public final class Endpointer {
    public let cfg: EndpointConfig
    private var pending: [Float] = []
    private var pre: [Float] = []
    private var buf: [Float] = []
    private var silent: Int = 0
    private var pos: Int = 0
    private var startSample: Int = 0

    public init(config: EndpointConfig = EndpointConfig()) {
        self.cfg = config
    }

    private func samplesToMs(_ n: Int) -> Int {
        n * 1000 / kTargetSampleRate
    }

    public func push(_ pcm: [Float], vad: VADModel) throws -> [UtteranceSegment] {
        pending.append(contentsOf: pcm)
        if pending.count < kVADBatchFrames * kVADFrameSamples {
            return []
        }
        let n = (pending.count / kVADFrameSamples) * kVADFrameSamples
        let chunk = Array(pending.prefix(n))
        pending.removeFirst(n)

        let probs = try vad.probs(pcm: chunk)
        var out: [UtteranceSegment] = []
        let maxPreSamples = cfg.preRollMs * kTargetSampleRate / 1000

        for (frameIdx, p) in probs.enumerated() {
            let fStart = frameIdx * kVADFrameSamples
            let frame = chunk[fStart..<(fStart + kVADFrameSamples)]
            pos += kVADFrameSamples

            let speaking = p >= (buf.isEmpty ? cfg.threshold : (cfg.threshold - 0.15))
            if buf.isEmpty && !speaking {
                pre.append(contentsOf: frame)
                if pre.count > maxPreSamples {
                    pre.removeFirst(pre.count - maxPreSamples)
                }
                continue
            }
            if buf.isEmpty {
                buf = pre
                pre = []
                startSample = max(0, pos - kVADFrameSamples - buf.count)
            }
            buf.append(contentsOf: frame)
            silent = speaking ? 0 : (silent + kVADFrameSamples)

            if samplesToMs(silent) >= cfg.silenceMs || samplesToMs(buf.count) >= cfg.maxMs {
                let keep = max(1, buf.count - min(silent, buf.count))
                let trimmed = Array(buf.prefix(keep))
                let segStartMs = samplesToMs(startSample)
                let segEndMs = samplesToMs(startSample + trimmed.count)
                let delayMs = max(0, samplesToMs(pos) - segEndMs)
                out.append(UtteranceSegment(samples: trimmed, startMs: segStartMs, endMs: segEndMs, vadDelayMs: delayMs))
                buf.removeAll(keepingCapacity: true)
                silent = 0
            }
        }
        return out
    }

    /// Flushes any open utterance at stream EOF (used by `--input-wav` and graceful shutdown).
    public func flush() -> UtteranceSegment? {
        guard !buf.isEmpty else { return nil }
        let keep = max(1, buf.count - min(silent, buf.count))
        let trimmed = Array(buf.prefix(keep))
        let seg = UtteranceSegment(
            samples: trimmed,
            startMs: samplesToMs(startSample),
            endMs: samplesToMs(startSample + trimmed.count)
        )
        buf.removeAll()
        silent = 0
        return seg
    }
}
