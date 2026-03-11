import Combine
import Foundation
import SwiftUI
import WebKit

@main
struct MenuBarChatApp: App {
  @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
  @StateObject private var viewModel: ChatViewModel

  init() {
    let viewModel = ChatViewModel()
    _viewModel = StateObject(wrappedValue: viewModel)
    appDelegate.serverManager = viewModel.serverManager
  }

  var body: some Scene {
    MenuBarExtra("Chat", systemImage: "bubble.left.and.bubble.right.fill") {
      ChatView(viewModel: viewModel)
        .frame(width: 640, height: 520)
        .onAppear {
          viewModel.startServerIfNeeded()
        }
    }
    .menuBarExtraStyle(.window)
  }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
  var serverManager: ServerManager?

  func applicationWillTerminate(_ notification: Notification) {
    serverManager?.stop()
  }
}

@MainActor
final class ChatViewModel: ObservableObject {
  @Published var statusMessage: String?
  @Published var uiURL: URL?
  @Published var showLogs = false

  let serverManager: ServerManager?
  let webViewStore = WebViewStore()
  private var cancellables = Set<AnyCancellable>()

  init() {
    do {
      let config = try AppConfig.load()
      let serverManager = ServerManager(config: config)
      self.serverManager = serverManager
      statusMessage = "Starting server..."
      serverManager.startIfNeeded()

      serverManager.$port
        .receive(on: DispatchQueue.main)
        .sink { [weak self] port in
          self?.updateURL(for: port)
        }
        .store(in: &cancellables)

      serverManager.$lastError
        .receive(on: DispatchQueue.main)
        .sink { [weak self] error in
          if let error {
            self?.statusMessage = error
          }
        }
        .store(in: &cancellables)
    } catch {
      serverManager = nil
      statusMessage = error.localizedDescription
    }
  }

  private func updateURL(for port: Int?) {
    guard let port else {
      // Only show "Starting server..." if there is no error already displayed.
      // Otherwise the error would be overwritten immediately and flash away.
      if serverManager?.lastError == nil {
        statusMessage = "Starting server..."
      }
      uiURL = nil
      return
    }
    statusMessage = nil
    let baseURL = URL(string: "http://localhost:\(port)")!
    var uiURL = baseURL.appendingPathComponent("ui")
    if var components = URLComponents(url: uiURL, resolvingAgainstBaseURL: false) {
      components.queryItems = [URLQueryItem(name: "embedded", value: "1")]
      uiURL = components.url ?? uiURL
    }
    self.uiURL = uiURL
  }

  func startServerIfNeeded() {
    serverManager?.startIfNeeded()
  }
}

struct ChatView: View {
  @ObservedObject var viewModel: ChatViewModel

  var body: some View {
    VStack(spacing: 0) {
      if viewModel.showLogs {
        LogPanelView(entries: viewModel.serverManager?.logEntries ?? [])
          .frame(maxHeight: 200)
          .transition(.move(edge: .top).combined(with: .opacity))
      }

      VStack(spacing: 12) {
        if let statusMessage = viewModel.statusMessage {
          Text(statusMessage)
            .font(.footnote)
            .foregroundColor(.red)
            .multilineTextAlignment(.leading)
            .frame(maxWidth: .infinity, alignment: .leading)
        }

        if let uiURL = viewModel.uiURL {
          WebView(url: uiURL, webViewStore: viewModel.webViewStore)
            .clipShape(RoundedRectangle(cornerRadius: 10))
        } else {
          Text("Chat UI unavailable.")
            .foregroundColor(.secondary)
        }
      }
      .padding(12)

      Divider()

      HStack {
        Button(action: {
          viewModel.webViewStore.reload()
        }) {
          Label("Reload", systemImage: "arrow.clockwise")
            .font(.footnote)
        }
        .buttonStyle(.borderless)

        Spacer()

        Button(action: {
          withAnimation(.easeInOut(duration: 0.2)) {
            viewModel.showLogs.toggle()
          }
        }) {
          Label(
            viewModel.showLogs ? "Hide Logs" : "Logs",
            systemImage: viewModel.showLogs ? "terminal.fill" : "terminal"
          )
          .font(.footnote)
        }
        .buttonStyle(.borderless)

        Spacer()

        Button(action: {
          NSApplication.shared.terminate(nil)
        }) {
          Label("Quit", systemImage: "xmark.circle")
            .font(.footnote)
        }
        .buttonStyle(.borderless)
      }
      .padding(.horizontal, 12)
      .padding(.vertical, 6)
    }
    .background(Color(nsColor: .windowBackgroundColor))
  }
}

// MARK: - Log panel

private let logTimeFmt: DateFormatter = {
  let f = DateFormatter()
  f.dateFormat = "HH:mm:ss"
  return f
}()

struct LogPanelView: View {
  let entries: [LogEntry]

  var body: some View {
    ScrollViewReader { proxy in
      ScrollView {
        LazyVStack(alignment: .leading, spacing: 2) {
          ForEach(entries) { entry in
            HStack(alignment: .top, spacing: 6) {
              Text(logTimeFmt.string(from: entry.timestamp))
                .foregroundColor(.secondary)
              Text(entry.stream.rawValue)
                .foregroundColor(streamColor(entry.stream))
                .frame(width: 42, alignment: .leading)
              Text(entry.text)
                .foregroundColor(streamColor(entry.stream))
            }
            .font(.system(size: 10, design: .monospaced))
            .id(entry.id)
          }
        }
        .padding(8)
      }
      .background(Color(nsColor: .textBackgroundColor))
      .onChange(of: entries.count) {
        if let last = entries.last {
          proxy.scrollTo(last.id, anchor: .bottom)
        }
      }
    }
  }

  private func streamColor(_ stream: LogEntry.Stream) -> Color {
    switch stream {
    case .stdout: return .primary
    case .stderr: return .orange
    case .system: return .secondary
    }
  }
}

struct WebView: NSViewRepresentable {
  let url: URL
  let webViewStore: WebViewStore

  func makeNSView(context: Context) -> WKWebView {
    let view = WKWebView()
    view.setValue(false, forKey: "drawsBackground")
    view.load(URLRequest(url: url))
    webViewStore.webView = view
    return view
  }

  func updateNSView(_ nsView: WKWebView, context: Context) {
    guard nsView.url != url else {
      return
    }
    nsView.load(URLRequest(url: url))
  }
}

@MainActor
final class WebViewStore: ObservableObject {
  var webView: WKWebView?

  func reload() {
    webView?.reload()
  }
}

struct AppConfig {
  let rootURL: URL

  static func load() throws -> AppConfig {
    // 1. Try to find the server binary next to our own executable.
    //    This supports the standard layout where both binaries live in bin/:
    //      quarry/
    //        bin/server
    //        bin/MenuBarChat
    //        datasources/datasources.json
    if let execURL = resolvedExecutableURL() {
      let binDir = execURL.deletingLastPathComponent()
      let serverPath = binDir.appendingPathComponent("server")
      if FileManager.default.fileExists(atPath: serverPath.path) {
        let rootURL = binDir.deletingLastPathComponent()
        return AppConfig(rootURL: rootURL)
      }
    }

    // 2. Fall back to CHATBOT_ROOT environment variable.
    let env = ProcessInfo.processInfo.environment
    let rootPath = env["CHATBOT_ROOT"]?.trimmingCharacters(in: .whitespacesAndNewlines)
    guard let rootPath, !rootPath.isEmpty else {
      throw ConfigError.serverNotFound
    }

    let rootURL = URL(fileURLWithPath: rootPath)
    let serverPath = rootURL.appendingPathComponent("bin/server")
    guard FileManager.default.fileExists(atPath: serverPath.path) else {
      throw ConfigError.invalidRoot(rootPath)
    }

    return AppConfig(rootURL: rootURL)
  }

  /// Resolve the path of the running executable, following symlinks.
  private static func resolvedExecutableURL() -> URL? {
    // Bundle.main.executableURL works inside .app bundles and for bare
    // executables launched via their full path.  For robustness, fall
    // back to argv[0] resolved through the file system.
    if let url = Bundle.main.executableURL {
      return url.resolvingSymlinksInPath()
    }
    let argv0 = CommandLine.arguments[0]
    let url = URL(fileURLWithPath: argv0).resolvingSymlinksInPath()
    guard FileManager.default.fileExists(atPath: url.path) else { return nil }
    return url
  }
}

enum ConfigError: LocalizedError {
  case serverNotFound
  case invalidRoot(String)

  var errorDescription: String? {
    switch self {
    case .serverNotFound:
      return
        "Could not find the server binary. Place it next to MenuBarChat (e.g. bin/server), or set CHATBOT_ROOT to the repo path containing bin/server."
    case .invalidRoot(let path):
      return "Root path does not contain bin/server: \(path)"
    }
  }
}

/// A single log entry from the server process.
struct LogEntry: Identifiable {
  enum Stream: String { case stdout, stderr, system }
  let id = UUID()
  let timestamp = Date()
  let stream: Stream
  let text: String
}

@MainActor
final class ServerManager: ObservableObject {
  private nonisolated static let portRegex = try! NSRegularExpression(
    pattern: #"Chat server listening on http://localhost:(\d+)/"#
  )

  private let config: AppConfig
  private var process: Process?
  private var restartTimer: Timer?
  private var isStarting = false
  private let restartDelay: TimeInterval = 2
  private var stdoutPipe: Pipe?
  private var stderrPipe: Pipe?

  /// Cap the log buffer to avoid unbounded memory growth.
  private let maxLogEntries = 500

  @Published var port: Int?
  @Published var lastError: String?
  @Published var logEntries: [LogEntry] = []

  /// Accumulated stderr output from the current server process.
  /// Surfaced as `lastError` only when the process exits with a
  /// non-zero code, so harmless output (version banners, warnings)
  /// does not show up as errors while the server is running.
  private var stderrBuffer: String?

  init(config: AppConfig) {
    self.config = config
  }

  func startIfNeeded() {
    guard !isStarting else {
      return
    }
    guard process?.isRunning != true else {
      return
    }
    startServer()
  }

  func stop() {
    restartTimer?.invalidate()
    restartTimer = nil
    stdoutPipe?.fileHandleForReading.readabilityHandler = nil
    stdoutPipe = nil
    stderrPipe?.fileHandleForReading.readabilityHandler = nil
    stderrPipe = nil
    process?.terminate()
    process = nil
  }

  private func startServer() {
    isStarting = true
    defer {
      isStarting = false
    }

    let serverBinary = config.rootURL.appendingPathComponent("bin/server")

    let process = Process()
    process.executableURL = serverBinary
    process.currentDirectoryURL = config.rootURL
    process.environment = ProcessInfo.processInfo.environment

    let stdoutPipe = Pipe()
    process.standardOutput = stdoutPipe
    self.stdoutPipe = stdoutPipe

    let stderrPipe = Pipe()
    process.standardError = stderrPipe
    self.stderrPipe = stderrPipe

    stdoutPipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
      let data = handle.availableData
      guard !data.isEmpty else { return }
      let output = String(data: data, encoding: .utf8) ?? ""
      self?.appendLog(output, stream: .stdout)
      self?.parsePort(from: output)
    }

    stderrPipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
      let data = handle.availableData
      guard !data.isEmpty else { return }
      let output = String(data: data, encoding: .utf8) ?? ""
      self?.appendLog(output, stream: .stderr)
      self?.parsePort(from: output)
      self?.captureError(from: output)
    }

    process.terminationHandler = { [weak self] proc in
      DispatchQueue.main.async {
        self?.handleTermination(exitCode: proc.terminationStatus)
      }
    }

    // Don't clear lastError here -- keep it visible until the new server
    // successfully starts (parsePort sets lastError = nil on success).

    appendLog("Starting server: \(serverBinary.path)", stream: .system)

    do {
      try process.run()
      self.process = process
    } catch {
      appendLog("Failed to launch: \(error.localizedDescription)", stream: .system)
      lastError = "Failed to launch server: \(error.localizedDescription)"
      handleTermination(exitCode: -1)
    }
  }

  private nonisolated func appendLog(_ text: String, stream: LogEntry.Stream) {
    let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
    guard !trimmed.isEmpty else { return }
    Task { @MainActor [weak self] in
      guard let self else { return }
      self.logEntries.append(LogEntry(stream: stream, text: trimmed))
      if self.logEntries.count > self.maxLogEntries {
        self.logEntries.removeFirst(self.logEntries.count - self.maxLogEntries)
      }
    }
  }

  private nonisolated func parsePort(from output: String) {
    guard let match = Self.portRegex.firstMatch(
        in: output, range: NSRange(output.startIndex..., in: output)),
      let portRange = Range(match.range(at: 1), in: output)
    else {
      return
    }
    let portString = String(output[portRange])
    guard let port = Int(portString) else { return }
    Task { @MainActor [weak self] in
      self?.port = port
      self?.lastError = nil
    }
  }

  private nonisolated func captureError(from output: String) {
    let trimmed = output.trimmingCharacters(in: .whitespacesAndNewlines)
    guard !trimmed.isEmpty else { return }
    // Don't treat the port-announcement line as an error.
    if trimmed.contains("Chat server listening on") { return }
    Task { @MainActor [weak self] in
      guard let self else { return }
      if let existing = self.stderrBuffer {
        self.stderrBuffer = existing + "\n" + trimmed
      } else {
        self.stderrBuffer = trimmed
      }
    }
  }

  private func handleTermination(exitCode: Int32 = 0) {
    stdoutPipe?.fileHandleForReading.readabilityHandler = nil
    stdoutPipe = nil
    stderrPipe?.fileHandleForReading.readabilityHandler = nil
    stderrPipe = nil
    process = nil
    port = nil

    appendLog("Server exited with code \(exitCode)", stream: .system)

    // Only surface stderr as an error when the process actually failed.
    if exitCode != 0 {
      if let stderr = stderrBuffer, !stderr.isEmpty {
        let lines = stderr.components(separatedBy: .newlines)
        lastError = lines.last { !$0.trimmingCharacters(in: .whitespaces).isEmpty }
          ?? "Server exited with code \(exitCode)"
      } else if lastError == nil {
        lastError = "Server exited with code \(exitCode)"
      }
    }
    stderrBuffer = nil
    scheduleRestart()
  }

  private func scheduleRestart() {
    guard restartTimer == nil else {
      return
    }
    restartTimer = Timer.scheduledTimer(withTimeInterval: restartDelay, repeats: false) {
      [weak self] _ in
      Task { @MainActor in
        guard let self else { return }
        self.restartTimer = nil
        self.startIfNeeded()
      }
    }
  }
}
