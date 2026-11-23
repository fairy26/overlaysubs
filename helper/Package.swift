// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "CaptureHelper",
    platforms: [
        .macOS(.v13),
    ],
    products: [
        .library(name: "CaptureCore", targets: ["CaptureCore"]),
        .executable(name: "capture-helper", targets: ["CaptureHelper"]),
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
            dependencies: ["CWhisper"],
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
