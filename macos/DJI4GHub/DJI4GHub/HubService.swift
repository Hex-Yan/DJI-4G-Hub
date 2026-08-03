import Foundation
import Combine

@MainActor
final class HubService: ObservableObject {

    @Published var isReady = false
    @Published var statusText = "正在检查 DJI 4G Hub..."
    @Published var errorText: String?

    private let dashboardURL = URL(string: "http://127.0.0.1:7575")!

    func start() async {

        if await dashboardIsAvailable() {
            isReady = true
            return
        }

        do {
            try launchNERV()
        } catch {
            errorText = error.localizedDescription
            return
        }

        for _ in 0..<30 {

            try? await Task.sleep(for: .milliseconds(500))

            if await dashboardIsAvailable() {
                isReady = true
                return
            }
        }

        errorText = "无法连接 DJI 4G Hub。"
    }

    private func dashboardIsAvailable() async -> Bool {

        do {

            var request = URLRequest(url: dashboardURL)
            request.timeoutInterval = 1

            let (_, response) = try await URLSession.shared.data(for: request)

            guard let http = response as? HTTPURLResponse else {
                return false
            }

            return http.statusCode == 200

        } catch {
            return false
        }
    }

    private func launchNERV() throws {
        let nervPath = "/usr/local/bin/nerv"

        guard FileManager.default.isExecutableFile(atPath: nervPath) else {
            throw HubServiceError.nervNotFound
        }

        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/bin/sh")
        process.arguments = [nervPath, "launch"]
        process.standardOutput = FileHandle.nullDevice
        process.standardError = FileHandle.nullDevice

        try process.run()
    }
    }

    enum HubServiceError: LocalizedError {
        case nervNotFound

        var errorDescription: String? {
            switch self {
            case .nervNotFound:
                return "找不到 /usr/local/bin/nerv，请先安装 NERV 命令。"
            }
        }
    }
