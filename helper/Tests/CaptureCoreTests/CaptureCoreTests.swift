import CaptureCore
import Foundation
import XCTest

private final class ScriptVAD: VADModel {
    private var probsQueue: [Float]

    init(_ probs: [Float]) {
        self.probsQueue = probs
    }

    func probs(pcm: [Float]) throws -> [Float] {
        let n = pcm.count / kVADFrameSamples
        let slice = Array(probsQueue.prefix(n))
        probsQueue.removeFirst(min(n, probsQueue.count))
        return slice
    }
}

final class CaptureCoreTests: XCTestCase {
    func testEndpointerSpeechSilenceAndPreRoll() throws {
        let ep = Endpointer(config: EndpointConfig(threshold: 0.5, silenceMs: 600, maxMs: 15_000, preRollMs: 200))
        let vad = EnergyVAD(rmsThreshold: 0.01)

        var out: [UtteranceSegment] = []
        out += try ep.push([Float](repeating: 0.0, count: 32 * kVADFrameSamples), vad: vad)
        XCTAssertTrue(out.isEmpty, "leading silence emits nothing")

        out += try ep.push([Float](repeating: 0.1, count: 32 * kVADFrameSamples), vad: vad)
        out += try ep.push([Float](repeating: 0.0, count: 32 * kVADFrameSamples), vad: vad)

        XCTAssertEqual(out.count, 1)
        let expectedPreRoll = 200 * kTargetSampleRate / 1000
        XCTAssertEqual(out[0].samples.count, expectedPreRoll + 32 * kVADFrameSamples)
    }

    func testEndpointerHysteresis() throws {
        // 0.4 does not start speech, 0.9 starts it, 0.4 keeps it going, 0.2 ends it after 600ms.
        var probs: [Float] = []
        probs += [Float](repeating: 0.4, count: 8)
        probs += [Float](repeating: 0.9, count: 8)
        probs += [Float](repeating: 0.4, count: 8)
        probs += [Float](repeating: 0.9, count: 8)
        probs += [Float](repeating: 0.2, count: 24)

        let ep = Endpointer(config: EndpointConfig(threshold: 0.5, silenceMs: 600, maxMs: 15_000, preRollMs: 0))
        let segs = try ep.push([Float](repeating: 0.3, count: 56 * kVADFrameSamples), vad: ScriptVAD(probs))
        XCTAssertEqual(segs.count, 1)
        XCTAssertEqual(segs[0].samples.count, 24 * kVADFrameSamples)
    }

    func testEndpointerForceCutAtMaxMs() throws {
        let ep = Endpointer(config: EndpointConfig())
        let vad = EnergyVAD(rmsThreshold: 0.01)
        let segs = try ep.push([Float](repeating: 0.1, count: kTargetSampleRate * 31), vad: vad)
        XCTAssertEqual(segs.count, 2)
    }

    func testWAVRoundTrip16kMonoAndResample48kStereo() throws {
        let tmpDir = FileManager.default.temporaryDirectory
        let path = tmpDir.appendingPathComponent("test-\(UUID().uuidString).wav").path
        defer { try? FileManager.default.removeItem(atPath: path) }

        let writer = try StreamingWAVWriter(path: path)
        let input: [Float] = (0..<1600).map { sin(Float($0) * 0.05) * 0.5 }
        writer.append(samples: input)
        writer.close()

        let readBack = try readWAVAs16kMono(path: path)
        XCTAssertEqual(readBack.count, input.count)
        for (a, b) in zip(input, readBack) {
            XCTAssertEqual(a, b, accuracy: 0.001)
        }

        // Resampler 48k stereo -> 16k mono
        let resampler = LinearResampler(inputRate: 48_000)
        var stereo = [Float](repeating: 0, count: 9600)
        for i in stride(from: 0, to: 9600, by: 2) {
            stereo[i] = 1.0
            stereo[i + 1] = 0.0
        }
        let a = resampler.pushMono(downmixInterleaved(stereo, channels: 2))
        let b = resampler.pushMono(downmixInterleaved(stereo, channels: 2))
        XCTAssertEqual(a.count + b.count, 3200)
        XCTAssertTrue(b.allSatisfy { abs($0 - 0.5) < 1e-5 })
    }

    func testProtocolJSONEncoding() throws {
        let ev = UtteranceEvent(id: 1, text: "Hello world.", startMs: 1200, endMs: 3400, asrMs: 180, engine: "whisper")
        let data = try JSONEncoder().encode(ev)
        let decoded = try JSONDecoder().decode(UtteranceEvent.self, from: data)
        XCTAssertEqual(decoded, ev)
        let str = String(data: data, encoding: .utf8)!
        XCTAssertTrue(str.contains("\"type\":\"utterance\""))
        XCTAssertTrue(str.contains("\"start_ms\":1200"))
        XCTAssertTrue(str.contains("\"end_ms\":3400"))
        XCTAssertTrue(str.contains("\"asr_ms\":180"))
        XCTAssertTrue(str.contains("\"engine\":\"whisper\""))
    }
}
