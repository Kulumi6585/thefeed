import SwiftUI

struct ContentView: View {
    @EnvironmentObject var server: ServerController

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            if server.port > 0 {
                WebView(url: URL(string: "http://127.0.0.1:\(server.port)")!)
                    .ignoresSafeArea()
            } else if let err = server.lastError {
                VStack(spacing: 12) {
                    Text("startup failed").font(.headline).foregroundColor(.white)
                    Text(err).font(.caption).foregroundColor(.secondary)
                    Button("retry") { server.start() }
                }
                .padding()
            } else {
                ProgressView()
            }
        }
    }
}
