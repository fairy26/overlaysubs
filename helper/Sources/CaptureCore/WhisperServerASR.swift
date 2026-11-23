import Foundation

/// ASR engine backed by a local whisper.cpp `whisper-server` HTTP endpoint (`POST /inference`).
public final class WhisperServerASR: ASREngine, @unchecked Sendable {
    public let engineName = "whisper"
    private let inferenceURL: URL
    private let healthURL: URL
    private let session: URLSession

    public init(baseURL: URL, session: URLSession = .shared) {
        let trimmed = baseURL.absoluteString.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        self.inferenceURL = URL(string: "\(trimmed)/inference")!
        self.healthURL = URL(string: "\(trimmed)/health")!
        self.session = session
    }

    public func prepare() async throws {
        var req = URLRequest(url: healthURL)
        req.timeoutInterval = 5
        let (data, resp) = try await session.data(for: req)
        guard let http = resp as? HTTPURLResponse, http.statusCode == 200 else {
            let code = (resp as? HTTPURLResponse)?.statusCode ?? -1
            let body = String(data: data, encoding: .utf8) ?? ""
            throw ASREngineError.httpError(code, body)
        }
    }

    public func transcribe(samples: [Float], language: String, prompt: String) async throws -> ASRResult {
        let started = CFAbsoluteTimeGetCurrent()
        let wavData = encodeWAV16kMonoPCM16(samples: samples)
        let boundary = "Boundary-\(UUID().uuidString)"

        var body = Data()
        appendFormField(&body, boundary: boundary, name: "response_format", value: "json")
        appendFormField(&body, boundary: boundary, name: "temperature", value: "0.0")
        appendFormField(&body, boundary: boundary, name: "temperature_inc", value: "0.0")
        if !language.isEmpty {
            appendFormField(&body, boundary: boundary, name: "language", value: language)
        }
        let cleanedPrompt = prompt.replacingOccurrences(of: "\0", with: "").trimmingCharacters(in: .whitespacesAndNewlines)
        if !cleanedPrompt.isEmpty {
            appendFormField(&body, boundary: boundary, name: "prompt", value: cleanedPrompt)
        }

        body.append("--\(boundary)\r\n".data(using: .utf8)!)
        body.append("Content-Disposition: form-data; name=\"file\"; filename=\"utterance.wav\"\r\n".data(using: .utf8)!)
        body.append("Content-Type: audio/wav\r\n\r\n".data(using: .utf8)!)
        body.append(wavData)
        body.append("\r\n--\(boundary)--\r\n".data(using: .utf8)!)

        var req = URLRequest(url: inferenceURL)
        req.httpMethod = "POST"
        req.timeoutInterval = 60
        req.setValue("multipart/form-data; boundary=\(boundary)", forHTTPHeaderField: "Content-Type")
        req.httpBody = body

        let (data, resp) = try await session.data(for: req)
        guard let http = resp as? HTTPURLResponse else {
            throw ASREngineError.invalidResponse("non-HTTP response")
        }
        guard (200..<300).contains(http.statusCode) else {
            let text = String(data: data, encoding: .utf8) ?? ""
            throw ASREngineError.httpError(http.statusCode, text)
        }

        struct WhisperJSON: Decodable {
            let text: String?
            let language: String?
        }

        let decoded: WhisperJSON
        do {
            decoded = try JSONDecoder().decode(WhisperJSON.self, from: data)
        } catch {
            let raw = String(data: data, encoding: .utf8) ?? ""
            throw ASREngineError.invalidResponse("JSON decode failed: \(error) (body: \(raw))")
        }

        let elapsedMs = max(0, Int(((CFAbsoluteTimeGetCurrent() - started) * 1000.0).rounded()))
        let text = (decoded.text ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        let detectedLang = (decoded.language?.isEmpty == false ? decoded.language! : (language.isEmpty ? "en" : language))
        return ASRResult(text: text, language: detectedLang, elapsedMs: elapsedMs)
    }

    private func appendFormField(_ body: inout Data, boundary: String, name: String, value: String) {
        body.append("--\(boundary)\r\n".data(using: .utf8)!)
        body.append("Content-Disposition: form-data; name=\"\(name)\"\r\n\r\n".data(using: .utf8)!)
        body.append("\(value)\r\n".data(using: .utf8)!)
    }
}
