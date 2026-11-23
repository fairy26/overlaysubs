import CoreML
import FluidAudio
import Foundation

/// ASR engine backed by NVIDIA Parakeet TDT 0.6B v3 via FluidAudio (Core ML / Apple Neural Engine).
public actor ParakeetASR: ASREngine {
    public nonisolated let engineName = "parakeet"

    private let modelDirectory: URL?
    private var manager: AsrManager?

    public init(modelDirectory: URL? = nil) {
        self.modelDirectory = modelDirectory
    }

    public func prepare() async throws {
        if manager != nil {
            return
        }
        let start = DispatchTime.now()
        // Explicitly target CPU + Apple Neural Engine so inference stays off the Metal GPU
        // (leaving GPU bandwidth free for PLaMo 3 Translate via llama-server).
        let mlConfig = AsrModels.defaultConfiguration()
        mlConfig.computeUnits = .cpuAndNeuralEngine

        logStderr("loading Parakeet TDT v3 (Core ML computeUnits=\(Self.describe(mlConfig.computeUnits)))")
        let models = try await AsrModels.downloadAndLoad(
            to: modelDirectory,
            configuration: mlConfig,
            version: .v3
        )
        let mgr = AsrManager(config: .default, models: models)
        self.manager = mgr

        let elapsedMs = Int((DispatchTime.now().uptimeNanoseconds - start.uptimeNanoseconds) / 1_000_000)
        let encUnits = models.encoder.map { Self.describe($0.configuration.computeUnits) } ?? "fused"
        let decUnits = Self.describe(models.decoder.configuration.computeUnits)
        let jointUnits = Self.describe(models.joint.configuration.computeUnits)
        let preUnits = Self.describe(models.preprocessor.configuration.computeUnits)
        logStderr(
            "Parakeet TDT v3 ready in \(elapsedMs)ms "
                + "(encoder=\(encUnits), decoder=\(decUnits), joint=\(jointUnits), "
                + "preprocessor=\(preUnits), encoderLadder=\(models.encoderLadder.count))"
        )
    }

    public func transcribe(samples: [Float], language: String, prompt: String) async throws -> CaptureCore.ASRResult {
        guard let mgr = manager else {
            throw ASREngineError.notImplemented("ParakeetASR.prepare() must be called before transcribe()")
        }
        guard !samples.isEmpty else {
            return CaptureCore.ASRResult(text: "", language: language, elapsedMs: 0)
        }

        // FluidAudio enforces a minimum sample count (~0.3s = 4800 samples at 16kHz);
        // right-pad short VAD utterances with silence so brief words never error out.
        let minSamples = ASRConstants.minimumRequiredSamples(forSampleRate: kTargetSampleRate)
        let inputSamples: [Float]
        if samples.count < minSamples {
            var padded = samples
            padded.append(contentsOf: repeatElement(0.0, count: minSamples - samples.count))
            inputSamples = padded
        } else {
            inputSamples = samples
        }

        let start = DispatchTime.now()
        var decoderState = try TdtDecoderState(decoderLayers: 2)
        let langHint = Language(rawValue: language.lowercased())
        let result = try await mgr.transcribe(inputSamples, decoderState: &decoderState, language: langHint)
        let elapsedMs = Int((DispatchTime.now().uptimeNanoseconds - start.uptimeNanoseconds) / 1_000_000)
        let cleaned = result.text.trimmingCharacters(in: .whitespacesAndNewlines)
        return CaptureCore.ASRResult(text: cleaned, language: language, elapsedMs: elapsedMs)
    }

    private static func describe(_ units: MLComputeUnits) -> String {
        switch units {
        case .cpuOnly: return "cpuOnly"
        case .cpuAndGPU: return "cpuAndGPU"
        case .all: return "all"
        case .cpuAndNeuralEngine: return "cpuAndNeuralEngine"
        @unknown default: return "unknown(\(units.rawValue))"
        }
    }
}
