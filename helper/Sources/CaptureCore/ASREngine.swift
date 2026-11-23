import Foundation

public struct ASRResult: Sendable, Equatable {
    public let text: String
    public let language: String
    public let elapsedMs: Int

    public init(text: String, language: String, elapsedMs: Int) {
        self.text = text
        self.language = language
        self.elapsedMs = elapsedMs
    }
}

/// Pluggable speech recognition interface shared by Whisper (Metal / whisper-server)
/// and Parakeet TDT v3 (Core ML / FluidAudio).
public protocol ASREngine: Sendable {
    var engineName: String { get }
    func prepare() async throws
    func transcribe(samples: [Float], language: String, prompt: String) async throws -> ASRResult
}

public enum ASREngineError: LocalizedError, Sendable {
    case unknownEngine(String)
    case notImplemented(String)
    case missingWhisperURL
    case httpError(Int, String)
    case invalidResponse(String)

    public var errorDescription: String? {
        switch self {
        case .unknownEngine(let name):
            return "unknown ASR engine: \(name) (expected whisper or parakeet)"
        case .notImplemented(let msg):
            return msg
        case .missingWhisperURL:
            return "--whisper-url is required when --asr whisper is selected"
        case .httpError(let code, let body):
            return "whisper-server returned HTTP \(code): \(body)"
        case .invalidResponse(let msg):
            return "invalid ASR response: \(msg)"
        }
    }
}

public func makeASREngine(kind: String, whisperURL: URL?) throws -> any ASREngine {
    switch kind.lowercased() {
    case "whisper":
        guard let url = whisperURL else {
            throw ASREngineError.missingWhisperURL
        }
        return WhisperServerASR(baseURL: url)
    case "parakeet":
        return ParakeetASR()
    default:
        throw ASREngineError.unknownEngine(kind)
    }
}
