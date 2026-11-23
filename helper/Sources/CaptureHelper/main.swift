import CaptureCore
import Foundation

struct CLIOptions {
    var asrEngine: String = "parakeet"
    var whisperURL: String = "http://127.0.0.1:18100"
    var vadModelPath: String = ""
    var appBundleId: String = ""
    var language: String = "en"
    var saveWavPath: String = ""
    var inputWavPath: String = ""
    var listApps: Bool = false
    var threshold: Float = 0.5
    var silenceMs: Int = 600
    var maxMs: Int = 15_000
    var preRollMs: Int = 200

    static func parse(_ args: [String]) throws -> CLIOptions {
        var opts = CLIOptions()
        var i = 1
        func nextValue(_ flag: String) throws -> String {
            i += 1
            guard i < args.count else {
                throw NSError(domain: "CLI", code: 1, userInfo: [NSLocalizedDescriptionKey: "missing value for \(flag)"])
            }
            return args[i]
        }
        while i < args.count {
            let arg = args[i]
            switch arg {
            case "--asr":
                opts.asrEngine = try nextValue(arg)
            case "--whisper-url":
                opts.whisperURL = try nextValue(arg)
            case "--vad-model":
                opts.vadModelPath = try nextValue(arg)
            case "--app":
                opts.appBundleId = try nextValue(arg)
            case "--language":
                opts.language = try nextValue(arg)
            case "--save-wav":
                opts.saveWavPath = try nextValue(arg)
            case "--input-wav":
                opts.inputWavPath = try nextValue(arg)
            case "--list-apps":
                opts.listApps = true
            case "--threshold":
                let v = try nextValue(arg)
                opts.threshold = Float(v) ?? opts.threshold
            case "--silence-ms":
                let v = try nextValue(arg)
                opts.silenceMs = Int(v) ?? opts.silenceMs
            case "--max-ms":
                let v = try nextValue(arg)
                opts.maxMs = Int(v) ?? opts.maxMs
            case "--pre-roll-ms":
                let v = try nextValue(arg)
                opts.preRollMs = Int(v) ?? opts.preRollMs
            case "-h", "--help":
                let usage = """
                usage: capture-helper [options]
                  --asr <parakeet|whisper>    ASR engine (default: parakeet)
                  --whisper-url <url>         whisper-server URL (default: http://127.0.0.1:18100)
                  --vad-model <path>          Silero VAD GGML model path (empty = RMS fallback)
                  --app <bundle-id>           Target macOS app bundle ID (empty = system audio)
                  --language <code>           Spoken language code (default: en)
                  --save-wav <path>           Save captured 16kHz mono audio to WAV
                  --input-wav <path>          Replay WAV file instead of live ScreenCaptureKit
                  --list-apps                 List capturable running apps as JSON and exit
                  --threshold <float>         VAD onset threshold (default: 0.5)
                  --silence-ms <int>          Silence duration to cut utterance (default: 600)
                  --max-ms <int>              Max utterance length in ms (default: 15000)
                  --pre-roll-ms <int>         Pre-roll buffer in ms (default: 200)
                """
                fputs(usage + "\n", stderr)
                Foundation.exit(0)
            default:
                throw NSError(domain: "CLI", code: 1, userInfo: [NSLocalizedDescriptionKey: "unknown argument: \(arg)"])
            }
            i += 1
        }
        return opts
    }
}

/// Keeps the last utterance text (up to 150 chars) as Whisper prompt so punctuation stays consistent.
actor UtteranceProcessor {
    private let engine: any ASREngine
    private let language: String
    private let writer: JSONLineWriter
    private var nextID: Int = 1
    private var lastPrompt: String = ""

    init(engine: any ASREngine, language: String, writer: JSONLineWriter) {
        self.engine = engine
        self.language = language
        self.writer = writer
    }

    func process(_ segment: UtteranceSegment) async {
        do {
            let res = try await engine.transcribe(
                samples: segment.samples,
                language: language,
                prompt: lastPrompt
            )
            let trimmed = res.text.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !trimmed.isEmpty else { return }

            let id = nextID
            nextID += 1
            writer.write(UtteranceEvent(
                id: id,
                text: trimmed,
                startMs: segment.startMs,
                endMs: segment.endMs,
                vadDelayMs: segment.vadDelayMs,
                asrMs: res.elapsedMs,
                engine: engine.engineName
            ))

            if trimmed.count > 150 {
                lastPrompt = String(trimmed.suffix(150))
            } else {
                lastPrompt = trimmed
            }
        } catch {
            logStderr("ASR error: \(error.localizedDescription)")
            writer.write(ErrorEvent(message: error.localizedDescription))
        }
    }
}

@main
struct CaptureHelperMain {
    static func main() async {
        let writer = JSONLineWriter()
        do {
            let opts = try CLIOptions.parse(CommandLine.arguments)

            if opts.listApps {
                let apps = try await ScreenAudioCapture.listApplications()
                let encoder = JSONEncoder()
                encoder.outputFormatting = [.withoutEscapingSlashes]
                let data = try encoder.encode(apps)
                FileHandle.standardOutput.write(data)
                FileHandle.standardOutput.write(Data([0x0A]))
                return
            }

            let whisperURL = URL(string: opts.whisperURL)
            let engine = try makeASREngine(kind: opts.asrEngine, whisperURL: whisperURL)
            try await engine.prepare()

            let vadModel: VADModel
            if !opts.vadModelPath.isEmpty {
                vadModel = try SileroVAD(modelPath: opts.vadModelPath)
                logStderr("loaded Silero VAD from \(opts.vadModelPath)")
            } else {
                vadModel = EnergyVAD(rmsThreshold: 0.01)
                logStderr("no --vad-model provided, using RMS energy VAD fallback")
            }

            let epConfig = EndpointConfig(
                threshold: opts.threshold,
                silenceMs: opts.silenceMs,
                maxMs: opts.maxMs,
                preRollMs: opts.preRollMs
            )
            let endpointer = Endpointer(config: epConfig)
            let processor = UtteranceProcessor(engine: engine, language: opts.language, writer: writer)

            // Mode 1: Replay WAV file for offline / benchmark testing
            if !opts.inputWavPath.isEmpty {
                let pcm = try readWAVAs16kMono(path: opts.inputWavPath)
                logStderr("loaded WAV \(opts.inputWavPath): \(pcm.count) samples (\(String(format: "%.2f", Double(pcm.count) / Double(kTargetSampleRate)))s)")
                if !opts.saveWavPath.isEmpty {
                    let saver = try StreamingWAVWriter(path: opts.saveWavPath)
                    saver.append(samples: pcm)
                    saver.close()
                    logStderr("saved 16kHz mono WAV to \(opts.saveWavPath)")
                }
                writer.write(ReadyEvent(engine: engine.engineName))

                let chunkSize = 160 // 10 ms chunks, matching live capture cadence
                var idx = 0
                while idx < pcm.count {
                    let end = min(pcm.count, idx + chunkSize)
                    let segs = try endpointer.push(Array(pcm[idx..<end]), vad: vadModel)
                    for seg in segs {
                        await processor.process(seg)
                    }
                    idx = end
                }
                // Feed 1 second of trailing silence so the final utterance closes naturally.
                let trailing = try endpointer.push([Float](repeating: 0, count: kTargetSampleRate), vad: vadModel)
                for seg in trailing {
                    await processor.process(seg)
                }
                if let rem = endpointer.flush() {
                    await processor.process(rem)
                }
                return
            }

            // Mode 2: Live ScreenCaptureKit audio capture
            var wavWriter: StreamingWAVWriter?
            if !opts.saveWavPath.isEmpty {
                wavWriter = try StreamingWAVWriter(path: opts.saveWavPath)
                logStderr("recording 16kHz mono WAV to \(opts.saveWavPath)")
            }

            let (segStream, segContinuation) = AsyncStream<UtteranceSegment>.makeStream()
            let vadLock = NSLock()
            let capture = ScreenAudioCapture(
                onSamples: { samples in
                    wavWriter?.append(samples: samples)
                    vadLock.lock()
                    defer { vadLock.unlock() }
                    do {
                        let segs = try endpointer.push(samples, vad: vadModel)
                        for seg in segs {
                            segContinuation.yield(seg)
                        }
                    } catch {
                        logStderr("VAD error: \(error.localizedDescription)")
                        writer.write(ErrorEvent(message: error.localizedDescription))
                    }
                },
                onError: { err in
                    logStderr("capture stream error: \(err.localizedDescription)")
                    writer.write(ErrorEvent(message: err.localizedDescription))
                    segContinuation.finish()
                }
            )

            try await capture.start(bundleIdentifier: opts.appBundleId.isEmpty ? nil : opts.appBundleId)
            writer.write(ReadyEvent(engine: engine.engineName))
            logStderr("capturing audio (app=\(opts.appBundleId.isEmpty ? "<system>" : opts.appBundleId), asr=\(engine.engineName))")

            // Graceful shutdown on SIGINT / SIGTERM
            signal(SIGINT, SIG_IGN)
            signal(SIGTERM, SIG_IGN)
            let sigInt = DispatchSource.makeSignalSource(signal: SIGINT, queue: .global())
            let sigTerm = DispatchSource.makeSignalSource(signal: SIGTERM, queue: .global())
            let stopOnce = NSLock()
            var stopped = false
            let triggerStop: () -> Void = {
                stopOnce.lock()
                defer { stopOnce.unlock() }
                guard !stopped else { return }
                stopped = true
                vadLock.lock()
                if let rem = endpointer.flush() {
                    segContinuation.yield(rem)
                }
                vadLock.unlock()
                segContinuation.finish()
            }
            sigInt.setEventHandler(handler: triggerStop)
            sigTerm.setEventHandler(handler: triggerStop)
            sigInt.resume()
            sigTerm.resume()

            for await seg in segStream {
                await processor.process(seg)
            }

            await capture.stop()
            wavWriter?.close()
            sigInt.cancel()
            sigTerm.cancel()
        } catch {
            let msg = error.localizedDescription
            logStderr("fatal: \(msg)")
            writer.write(ErrorEvent(message: msg))
            Foundation.exit(1)
        }
    }
}
