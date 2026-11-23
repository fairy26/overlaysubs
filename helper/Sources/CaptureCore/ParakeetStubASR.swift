import Foundation

/// Placeholder for Parakeet TDT v3 (FluidAudio / Core ML / Neural Engine).
/// Replace with the FluidAudio implementation while conforming to `ASREngine`.
public final class ParakeetStubASR: ASREngine, @unchecked Sendable {
    public let engineName = "parakeet"

    public init() {}

    public func prepare() async throws {
        throw ASREngineError.notImplemented(
            "parakeet ASR engine is not integrated yet; switch to --asr whisper or integrate FluidAudio"
        )
    }

    public func transcribe(samples: [Float], language: String, prompt: String) async throws -> ASRResult {
        throw ASREngineError.notImplemented(
            "parakeet ASR engine is not integrated yet"
        )
    }
}
