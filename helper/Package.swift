// swift-tools-version: 6.2
import PackageDescription

let package = Package(
    name: "CaptureHelper",
    platforms: [
        .macOS(.v14),
    ],
    products: [
        .library(name: "CaptureCore", targets: ["CaptureCore"]),
        .executable(name: "capture-helper", targets: ["CaptureHelper"]),
    ],
    dependencies: [
        // Opt out of TTS, Diarizer, and NemoTextProcessing binary to keep ASR-only footprint minimal.
        .package(url: "https://github.com/FluidInference/FluidAudio.git", from: "0.18.4", traits: []),
    ],
    targets: [
        .systemLibrary(
            name: "CWhisper",
            path: "Sources/CWhisper",
            pkgConfig: "whisper",
            providers: [
                .brew(["whisper.cpp"]),
            ]
        ),
        .target(
            name: "CaptureCore",
            dependencies: [
                "CWhisper",
                .product(name: "FluidAudio", package: "FluidAudio"),
            ],
            path: "Sources/CaptureCore",
            swiftSettings: [
                .swiftLanguageMode(.v5),
                .unsafeFlags(["-Xcc", "-I/opt/homebrew/include"]),
            ],
            linkerSettings: [
                .linkedFramework("ScreenCaptureKit"),
                .linkedFramework("CoreMedia"),
                .linkedFramework("CoreAudio"),
                .linkedFramework("AVFoundation"),
                .linkedFramework("CoreML"),
                .unsafeFlags(["-L/opt/homebrew/lib", "-Xlinker", "-rpath", "-Xlinker", "/opt/homebrew/lib"]),
            ]
        ),
        .executableTarget(
            name: "CaptureHelper",
            dependencies: ["CaptureCore"],
            path: "Sources/CaptureHelper",
            swiftSettings: [
                .swiftLanguageMode(.v5),
                .unsafeFlags(["-Xcc", "-I/opt/homebrew/include"]),
            ]
        ),
        .testTarget(
            name: "CaptureCoreTests",
            dependencies: ["CaptureCore"],
            path: "Tests/CaptureCoreTests",
            swiftSettings: [
                .swiftLanguageMode(.v5),
                .unsafeFlags(["-Xcc", "-I/opt/homebrew/include"]),
            ]
        ),
    ]
)
