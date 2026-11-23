import Foundation

public let kTargetSampleRate: Int = 16_000

/// Linear resampler to 16 kHz mono matching `crates/core/src/audio.rs`.
public final class LinearResampler: @unchecked Sendable {
    private let step: Double
    private var pos: Double = 0.0
    private var prev: Float = 0.0

    public init(inputRate: Int) {
        self.step = Double(max(1, inputRate)) / Double(kTargetSampleRate)
    }

    public func pushMono(_ mono: [Float]) -> [Float] {
        guard !mono.isEmpty else { return [] }
        if step == 1.0 && pos == 0.0 {
            prev = mono[mono.count - 1]
            return mono
        }
        var out: [Float] = []
        out.reserveCapacity(Int(Double(mono.count) / step) + 2)
        let countD = Double(mono.count)
        while pos < countD {
            let idx = Int(floor(pos))
            let frac = Float(pos - Double(idx))
            let a: Float = (idx == 0) ? prev : mono[idx - 1]
            let b: Float = mono[idx]
            out.append(a + (b - a) * frac)
            pos += step
        }
        pos -= countD
        prev = mono[mono.count - 1]
        return out
    }
}

public func downmixInterleaved(_ samples: [Float], channels: Int) -> [Float] {
    guard channels > 1 else { return samples }
    let frames = samples.count / channels
    guard frames > 0 else { return [] }
    var out = [Float](repeating: 0, count: frames)
    let inv = 1.0 / Float(channels)
    for f in 0..<frames {
        var sum: Float = 0
        let base = f * channels
        for c in 0..<channels {
            sum += samples[base + c]
        }
        out[f] = sum * inv
    }
    return out
}

public func downmixPlanar(_ planes: [[Float]]) -> [Float] {
    guard !planes.isEmpty else { return [] }
    let frames = planes.map(\.count).min() ?? 0
    guard frames > 0 else { return [] }
    var out = [Float](repeating: 0, count: frames)
    let inv = 1.0 / Float(planes.count)
    for plane in planes {
        for i in 0..<frames {
            out[i] += plane[i]
        }
    }
    for i in 0..<frames {
        out[i] *= inv
    }
    return out
}

/// Encodes 16 kHz mono float32 samples [-1, 1] into a complete 16-bit PCM WAV byte buffer.
public func encodeWAV16kMonoPCM16(samples: [Float]) -> Data {
    let dataSize = UInt32(samples.count * 2)
    var data = Data(capacity: 44 + Int(dataSize))
    appendWAVHeader(&data, sampleRate: UInt32(kTargetSampleRate), channels: 1, bitsPerSample: 16, dataSize: dataSize)
    for s in samples {
        let clamped = max(-1.0, min(1.0, s))
        var i16 = Int16((clamped * 32767.0).rounded()).littleEndian
        withUnsafeBytes(of: &i16) { data.append(contentsOf: $0) }
    }
    return data
}

private func appendWAVHeader(_ data: inout Data, sampleRate: UInt32, channels: UInt16, bitsPerSample: UInt16, dataSize: UInt32) {
    let byteRate = sampleRate * UInt32(channels) * UInt32(bitsPerSample / 8)
    let blockAlign = channels * (bitsPerSample / 8)
    let riffSize = 36 &+ dataSize

    func appendU32(_ v: UInt32) {
        var le = v.littleEndian
        withUnsafeBytes(of: &le) { data.append(contentsOf: $0) }
    }
    func appendU16(_ v: UInt16) {
        var le = v.littleEndian
        withUnsafeBytes(of: &le) { data.append(contentsOf: $0) }
    }

    data.append(contentsOf: [0x52, 0x49, 0x46, 0x46]) // RIFF
    appendU32(riffSize)
    data.append(contentsOf: [0x57, 0x41, 0x56, 0x45]) // WAVE
    data.append(contentsOf: [0x66, 0x6D, 0x74, 0x20]) // fmt
    appendU32(16)                                      // PCM fmt chunk size
    appendU16(1)                                       // PCM format
    appendU16(channels)
    appendU32(sampleRate)
    appendU32(byteRate)
    appendU16(blockAlign)
    appendU16(bitsPerSample)
    data.append(contentsOf: [0x64, 0x61, 0x74, 0x61]) // data
    appendU32(dataSize)
}

/// Streaming 16 kHz mono 16-bit PCM WAV writer for `--save-wav`.
public final class StreamingWAVWriter: @unchecked Sendable {
    private let lock = NSLock()
    private let handle: FileHandle
    private var bytesWritten: UInt64 = 0
    private var closed = false

    public init(path: String) throws {
        let url = URL(fileURLWithPath: path)
        let parent = url.deletingLastPathComponent()
        try FileManager.default.createDirectory(at: parent, withIntermediateDirectories: true)
        FileManager.default.createFile(atPath: path, contents: nil)
        let fh = try FileHandle(forWritingTo: url)
        var header = Data()
        appendWAVHeader(&header, sampleRate: UInt32(kTargetSampleRate), channels: 1, bitsPerSample: 16, dataSize: 0)
        try fh.write(contentsOf: header)
        self.handle = fh
    }

    public func append(samples: [Float]) {
        guard !samples.isEmpty else { return }
        lock.lock()
        defer { lock.unlock() }
        guard !closed else { return }
        var buf = Data(capacity: samples.count * 2)
        for s in samples {
            let clamped = max(-1.0, min(1.0, s))
            var i16 = Int16((clamped * 32767.0).rounded()).littleEndian
            withUnsafeBytes(of: &i16) { buf.append(contentsOf: $0) }
        }
        do {
            try handle.write(contentsOf: buf)
            bytesWritten += UInt64(buf.count)
        } catch {
            logStderr("wav write error: \(error)")
        }
    }

    public func close() {
        lock.lock()
        defer { lock.unlock() }
        guard !closed else { return }
        closed = true
        let clampedDataSize = UInt32(min(bytesWritten, UInt64(UInt32.max - 36)))
        var header = Data()
        appendWAVHeader(&header, sampleRate: UInt32(kTargetSampleRate), channels: 1, bitsPerSample: 16, dataSize: clampedDataSize)
        do {
            try handle.seek(toOffset: 0)
            try handle.write(contentsOf: header)
            try handle.close()
        } catch {
            logStderr("wav close error: \(error)")
        }
    }

    deinit {
        close()
    }
}

public enum WAVError: LocalizedError {
    case invalidFile(String)
    case unsupportedFormat(String)

    public var errorDescription: String? {
        switch self {
        case .invalidFile(let msg): return "invalid WAV file: \(msg)"
        case .unsupportedFormat(let msg): return "unsupported WAV format: \(msg)"
        }
    }
}

/// Reads a WAV file (PCM 16-bit / 24-bit / 32-bit or IEEE float 32-bit, any channel count or sample rate)
/// and returns 16 kHz mono float32 samples.
public func readWAVAs16kMono(path: String) throws -> [Float] {
    let data = try Data(contentsOf: URL(fileURLWithPath: path))
    guard data.count >= 44 else {
        throw WAVError.invalidFile("file shorter than 44 bytes")
    }
    let riff = String(data: data[0..<4], encoding: .ascii)
    let wave = String(data: data[8..<12], encoding: .ascii)
    guard riff == "RIFF", wave == "WAVE" else {
        throw WAVError.invalidFile("missing RIFF/WAVE header")
    }

    func readU16(_ offset: Int) -> UInt16 {
        UInt16(data[offset]) | (UInt16(data[offset + 1]) << 8)
    }
    func readU32(_ offset: Int) -> UInt32 {
        UInt32(data[offset])
            | (UInt32(data[offset + 1]) << 8)
            | (UInt32(data[offset + 2]) << 16)
            | (UInt32(data[offset + 3]) << 24)
    }

    var audioFormat: UInt16 = 0
    var channels: Int = 0
    var sampleRate: Int = 0
    var bitsPerSample: Int = 0
    var pcmRange: Range<Int>?

    var offset = 12
    while offset + 8 <= data.count {
        let chunkID = String(data: data[offset..<(offset + 4)], encoding: .ascii) ?? ""
        let chunkSize = Int(readU32(offset + 4))
        let bodyStart = offset + 8
        let bodyEnd = min(data.count, bodyStart + chunkSize)

        if chunkID == "fmt " && bodyEnd - bodyStart >= 16 {
            audioFormat = readU16(bodyStart)
            channels = Int(readU16(bodyStart + 2))
            sampleRate = Int(readU32(bodyStart + 4))
            bitsPerSample = Int(readU16(bodyStart + 14))
        } else if chunkID == "data" {
            pcmRange = bodyStart..<bodyEnd
            break
        }
        // Chunks are word-aligned.
        let padded = chunkSize + (chunkSize % 2)
        offset = bodyStart + padded
    }

    guard let range = pcmRange, channels > 0, sampleRate > 0 else {
        throw WAVError.invalidFile("missing fmt or data chunk")
    }

    var interleaved: [Float] = []
    let byteCount = range.count
    switch (audioFormat, bitsPerSample) {
    case (1, 16):
        let count = byteCount / 2
        interleaved.reserveCapacity(count)
        var i = range.lowerBound
        for _ in 0..<count {
            let u = UInt16(data[i]) | (UInt16(data[i + 1]) << 8)
            let s = Int16(bitPattern: u)
            interleaved.append(Float(s) / 32768.0)
            i += 2
        }
    case (3, 32):
        let count = byteCount / 4
        interleaved.reserveCapacity(count)
        var i = range.lowerBound
        for _ in 0..<count {
            let bits = readU32(i)
            interleaved.append(Float(bitPattern: bits))
            i += 4
        }
    case (1, 32):
        let count = byteCount / 4
        interleaved.reserveCapacity(count)
        var i = range.lowerBound
        for _ in 0..<count {
            let s = Int32(bitPattern: readU32(i))
            interleaved.append(Float(Double(s) / 2147483648.0))
            i += 4
        }
    default:
        throw WAVError.unsupportedFormat("format=\(audioFormat), bits=\(bitsPerSample)")
    }

    let mono = downmixInterleaved(interleaved, channels: channels)
    if sampleRate == kTargetSampleRate {
        return mono
    }
    let resampler = LinearResampler(inputRate: sampleRate)
    return resampler.pushMono(mono)
}
