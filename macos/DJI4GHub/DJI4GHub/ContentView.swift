//
//  ContentView.swift
//  DJI 4G Hub App
//
//  Created by 甄纪允 on 2026/7/31.
//

import SwiftUI
import WebKit
struct WebView: NSViewRepresentable {

    let url: URL

    func makeNSView(context: Context) -> WKWebView {
        let webView = WKWebView()
        webView.load(URLRequest(url: url))
        return webView
    }

    func updateNSView(_ nsView: WKWebView, context: Context) {
    }
}
struct ContentView: View {
    @StateObject private var hubService = HubService()

    private let dashboardURL =
        URL(string: "http://127.0.0.1:7575")!

    var body: some View {
        Group {
            if hubService.isReady {
                WebView(url: dashboardURL)
            } else {
                VStack(spacing: 20) {
                    ProgressView()

                    Text(hubService.statusText)

                    if let error = hubService.errorText {
                        Text(error)

                        Button("重新尝试") {
                            Task {
                                await hubService.start()
                            }
                        }
                    }
                }
            }
        }
        .frame(minWidth: 900, minHeight: 600)
        .task {
            await hubService.start()
        }
    }
}
#Preview {
    ContentView()
}
