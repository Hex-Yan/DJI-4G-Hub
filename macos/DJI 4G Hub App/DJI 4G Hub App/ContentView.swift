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

    var body: some View {

        WebView(
            url: URL(string: "http://127.0.0.1:7575")!
        )
    }
}

#Preview {
    ContentView()
}
