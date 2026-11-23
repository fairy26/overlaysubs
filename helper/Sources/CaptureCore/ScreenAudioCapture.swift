import CoreMedia
import Foundation
import ScreenCaptureKit

public enum CaptureError: LocalizedError {
    case permissionDenied(String)
    case noDisplay
    case appNotRunning(String)
    case streamFailed(String)

    public var errorDescription: String? {
        switch self {
        case .permissionDenied(let detail):
            return "E_MACOS_PERMISSION: Screen & System Audio Recording permission required (\(detail))"
        case .noDisplay:
            return "E_MACOS_NO_DISPLAY: no display available for ScreenCaptureKit"
        case .appNotRunning(let bundleId):
            return "E_APP_NOT_RUNNING: target application '\(bundleId)' is not running"
        case .streamFailed(let detail):
            return "E_MACOS_AUDIO: \(detail)"
        }
    }
}

public final class ScreenAudioCapture: NSObject, SCStreamOutput, SCStreamDelegate, @unchecked Sendable {
    private let onSamples: ([Float]) -> Void
    private let onError: (Error) -> Void
    private let audioQueue = DispatchQueue(label: "io.github.overlaysubs.sck-audio")
    private var stream: SCStream?
    private var inRate: Int = kTargetSampleRate
    private var resampler = LinearResampler(inputRate: kTargetSampleRate)

    public init(onSamples: @escaping ([Float]) -> Void, onError: @escaping (Error) -> Void) {
        self.onSamples = onSamples
        self.onError = onError
        super.init()
    }

    public static func listApplications() async throws -> [AppEntry] {
        let content: SCShareableContent
        do {
            content = try await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: false)
        } catch {
            throw mapSCError(error)
        }
        var seen = Set<String>()
        var apps: [AppEntry] = []
        for app in content.applications {
            let bid = app.bundleIdentifier
            let name = app.applicationName
            guard !bid.isEmpty, !name.isEmpty, seen.insert(bid).inserted else { continue }
            apps.append(AppEntry(bundleId: bid, name: name))
        }
        apps.sort { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
        return apps
    }

    public func start(bundleIdentifier: String?) async throws {
        let content: SCShareableContent
        do {
            content = try await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: false)
        } catch {
            throw Self.mapSCError(error)
        }

        guard let display = content.displays.first else {
            throw CaptureError.noDisplay
        }

        let filter: SCContentFilter
        if let bid = bundleIdentifier, !bid.isEmpty {
            let matching = content.applications.filter { $0.bundleIdentifier == bid }
            guard !matching.isEmpty else {
                throw CaptureError.appNotRunning(bid)
            }
            filter = SCContentFilter(display: display, including: matching, exceptingWindows: [])
        } else {
            filter = SCContentFilter(display: display, excludingWindows: [])
        }

        let config = SCStreamConfiguration()
        // Audio-only capture: tiny dummy frame since no screen output handler is registered.
        config.width = 2
        config.height = 2
        config.showsCursor = false
        config.capturesAudio = true
        config.sampleRate = kTargetSampleRate
        config.channelCount = 1
        config.excludesCurrentProcessAudio = true

        let scStream = SCStream(filter: filter, configuration: config, delegate: self)
        do {
            try scStream.addStreamOutput(self, type: .audio, sampleHandlerQueue: audioQueue)
            try await scStream.startCapture()
        } catch {
            throw Self.mapSCError(error)
        }
        self.stream = scStream
    }

    public func stop() async {
        guard let scStream = stream else { return }
        stream = nil
        do {
            try await scStream.stopCapture()
        } catch {
            logStderr("stopCapture error: \(error)")
        }
    }

    public func stream(_ stream: SCStream, didStopWithError error: Error) {
        onError(Self.mapSCError(error))
    }

    public func stream(_ stream: SCStream, didOutputSampleBuffer sampleBuffer: CMSampleBuffer, of type: SCStreamOutputType) {
        guard type == .audio else { return }
        guard CMSampleBufferIsValid(sampleBuffer), CMSampleBufferDataIsReady(sampleBuffer) else { return }
        guard let (mono, rate) = Self.extractMonoFloat32(from: sampleBuffer) else { return }

        if rate != inRate {
            inRate = rate
            resampler = LinearResampler(inputRate: rate)
        }
        let resampled = resampler.pushMono(mono)
        if !resampled.isEmpty {
            onSamples(resampled)
        }
    }

    private static func extractMonoFloat32(from sampleBuffer: CMSampleBuffer) -> ([Float], Int)? {
        guard let desc = CMSampleBufferGetFormatDescription(sampleBuffer),
              let asbdPtr = CMAudioFormatDescriptionGetStreamBasicDescription(desc) else {
            return nil
        }
        let asbd = asbdPtr.pointee
        let isFloat = (asbd.mFormatFlags & kAudioFormatFlagIsFloat) != 0
        guard isFloat else { return nil }
        let isBigEndian = (asbd.mFormatFlags & kAudioFormatFlagIsBigEndian) != 0
        let rate = max(1, Int(asbd.mSampleRate.rounded()))

        var neededSize = 0
        let sizeStatus = CMSampleBufferGetAudioBufferListWithRetainedBlockBuffer(
            sampleBuffer,
            bufferListSizeNeededOut: &neededSize,
            bufferListOut: nil,
            bufferListSize: 0,
            blockBufferAllocator: nil,
            blockBufferMemoryAllocator: nil,
            flags: 0,
            blockBufferOut: nil
        )
        guard sizeStatus == noErr, neededSize > 0 else { return nil }

        let rawMem = UnsafeMutableRawPointer.allocate(
            byteCount: neededSize,
            alignment: MemoryLayout<AudioBufferList>.alignment
        )
        defer { rawMem.deallocate() }
        let ablPtr = rawMem.bindMemory(to: AudioBufferList.self, capacity: 1)

        var blockBuffer: CMBlockBuffer?
        let status = CMSampleBufferGetAudioBufferListWithRetainedBlockBuffer(
            sampleBuffer,
            bufferListSizeNeededOut: nil,
            bufferListOut: ablPtr,
            bufferListSize: neededSize,
            blockBufferAllocator: nil,
            blockBufferMemoryAllocator: nil,
            flags: kCMSampleBufferFlag_AudioBufferList_Assure16ByteAlignment,
            blockBufferOut: &blockBuffer
        )
        guard status == noErr else { return nil }

        let abl = UnsafeMutableAudioBufferListPointer(ablPtr)
        var planes: [[Float]] = []
        planes.reserveCapacity(abl.count)

        for audioBuf in abl {
            guard let dataPtr = audioBuf.mData, audioBuf.mDataByteSize >= 4 else { continue }
            let count = Int(audioBuf.mDataByteSize) / 4
            let channels = max(1, Int(audioBuf.mNumberChannels))
            var floats = [Float](repeating: 0, count: count)
            if isBigEndian {
                let u32Ptr = dataPtr.bindMemory(to: UInt32.self, capacity: count)
                for i in 0..<count {
                    floats[i] = Float(bitPattern: UInt32(bigEndian: u32Ptr[i]))
                }
            } else {
                let f32Ptr = dataPtr.bindMemory(to: Float.self, capacity: count)
                for i in 0..<count {
                    floats[i] = f32Ptr[i]
                }
            }
            if channels > 1 {
                planes.append(downmixInterleaved(floats, channels: channels))
            } else {
                planes.append(floats)
            }
        }

        let mono: [Float]
        if planes.isEmpty {
            return nil
        } else if planes.count == 1 {
            mono = planes[0]
        } else {
            mono = downmixPlanar(planes)
        }
        return mono.isEmpty ? nil : (mono, rate)
    }

    private static func mapSCError(_ error: Error) -> CaptureError {
        let lower = error.localizedDescription.lowercased()
        let permissionHints = ["declined", "tcc", "permission", "not authorized", "unauthorized", "screen recording"]
        if permissionHints.contains(where: { lower.contains($0) }) {
            return .permissionDenied(error.localizedDescription)
        }
        return .streamFailed(error.localizedDescription)
    }
}
