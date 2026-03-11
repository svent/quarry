// swift-tools-version: 5.9
import PackageDescription

let package = Package(
  name: "MenuBarChat",
  platforms: [
    .macOS(.v14),
  ],
  products: [
    .executable(
      name: "MenuBarChat",
      targets: ["MenuBarChat"]
    ),
  ],
  dependencies: [],
  targets: [
    .executableTarget(
      name: "MenuBarChat",
      dependencies: []
    ),
  ]
)
