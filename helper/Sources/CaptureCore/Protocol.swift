import Foundation

public struct ReadyEvent: Codable, Sendable, Equatable {
    public let type: String
    public let engine: String

    public init(engine: String) {
        self.type = "ready"
        self.engine = engine
    }
}

public struct UtteranceEvent: Codable, Sendable, Equatable {
    public let type: String
    public let id: Int
    public let text: String
    public let startMs: Int
    public let endMs: Int
    public let asrMs: Int
    public let engine: String

    enum CodingKeys: String, CodingKey {
        case type
        case id
        case text
        case startMs = "start_ms"
        case endMs = "end_ms"
        case asrMs = "asr_ms"
        case engine
    }

    public init(id: Int, text: String, startMs: Int, endMs: Int, asrMs: Int, engine: String) {
        self.type = "utterance"
        self.id = id
        self.text = text
        self.startMs = startMs
        self.endMs = endMs
        self.asrMs = asrMs
        self.engine = engine
    }
}

public struct ErrorEvent: Codable, Sendable, Equatable {
    public let type: String
    public let message: String

    public init(message: String) {
        self.type = "error"
        self.message = message
    }
}

public struct AppEntry: Codable, Sendable, Equatable {
    public let bundleId: String
    public let name: String

    enum CodingKeys: String, CodingKey {
        case bundleId = "bundle_id"
        case name
    }

    public init(bundleId: String, name: String) {
        self.bundleId = bundleId
        self.name = name
    }
}

public final class JSONLineWriter: @unchecked Sendable {
    private let lock = NSLock()
    private let encoder: JSONEncoder
    private let handle: FileHandle

    public init(handle: FileHandle = .standardOutput) {
        self.handle = handle
        let enc = JSONEncoder()
        enc.outputFormatting = [.withoutEscapingSlashes]
        self.encoder = enc
    }

    public func write<T: Encodable>(_ value: T) {
        lock.lock()
        defer { lock.unlock() }
        do {
            var data = try encoder.encode(value)
            data.append(0x0A) // '\n'
            try handle.write(contentsOf: data)
        } catch {
            logStderr("failed to write json line: \(error)")
        }
    }
}

public func logStderr(_ message: String) {
    let line = "[capture-helper] \(message)\n"
    if let data = line.data(using: .utf8) {
        try? FileHandle.standardError.write(contentsOf: data)
    }
}
