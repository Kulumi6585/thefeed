package com.thefeed.android

import android.Manifest
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.TextView
import androidx.activity.ComponentActivity
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.content.ContextCompat
import java.net.HttpURLConnection
import java.net.URL

class MainActivity : ComponentActivity() {
    private lateinit var webView: WebView
    private lateinit var txtStatus: TextView
    private val handler = Handler(Looper.getMainLooper())
    private var probeAttempt = 0

    private val notificationPermissionLauncher = registerForActivityResult(
        ActivityResultContracts.RequestPermission()
    ) { /* granted or not, service still works */ }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        webView = findViewById(R.id.webView)
        txtStatus = findViewById(R.id.txtStatus)

        requestNotificationPermission()
        configureWebView()
        startThefeedService()
        probeAttempt = 0
        waitForServerThenLoad()
    }

    private fun requestNotificationPermission() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            if (ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS)
                != PackageManager.PERMISSION_GRANTED
            ) {
                notificationPermissionLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
            }
        }
    }

    private fun startThefeedService() {
        val intent = Intent(this, ThefeedService::class.java)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            startForegroundService(intent)
        } else {
            startService(intent)
        }
    }

    private fun configureWebView() {
        webView.webViewClient = object : WebViewClient() {
            override fun onPageFinished(view: WebView?, url: String?) {
                if (url != null && url.startsWith("http://127.0.0.1")) {
                    txtStatus.text = ""
                }
            }

            override fun onReceivedError(
                view: WebView?,
                request: WebResourceRequest?,
                error: WebResourceError?
            ) {
                // Server was reachable during probe but dropped connection — retry probe cycle
                if (request?.isForMainFrame == true) {
                    probeAttempt = 0
                    txtStatus.text = "Reconnecting..."
                    handler.postDelayed({ waitForServerThenLoad() }, RETRY_DELAY_MS)
                }
            }
        }

        with(webView.settings) {
            javaScriptEnabled = true
            domStorageEnabled = true
            cacheMode = WebSettings.LOAD_NO_CACHE
            allowFileAccess = false
            allowContentAccess = false
            mixedContentMode = WebSettings.MIXED_CONTENT_NEVER_ALLOW
        }
    }

    /**
     * Polls the server in a background thread until it responds with HTTP 200 (or any
     * response — a TCP connection refused is what we're avoiding). Only then hands the
     * URL to WebView, ensuring it never shows a browser error page on startup.
     */
    private fun waitForServerThenLoad() {
        val port = getCurrentPort()
        if (port <= 0) {
            if (probeAttempt < MAX_PROBE_ATTEMPTS) {
                probeAttempt++
                txtStatus.text = "Waiting for service... ($probeAttempt/$MAX_PROBE_ATTEMPTS)"
                handler.postDelayed({ waitForServerThenLoad() }, PROBE_INTERVAL_MS)
            } else {
                txtStatus.text = "Service unavailable. Restart the app."
            }
            return
        }

        val url = "http://127.0.0.1:$port"
        txtStatus.text = "Connecting..."

        Thread {
            var ready = false
            repeat(MAX_PROBE_ATTEMPTS) { attempt ->
                if (ready) return@repeat
                try {
                    val conn = URL(url).openConnection() as HttpURLConnection
                    conn.connectTimeout = PROBE_TIMEOUT_MS.toInt()
                    conn.readTimeout = PROBE_TIMEOUT_MS.toInt()
                    conn.requestMethod = "GET"
                    val code = conn.responseCode
                    conn.disconnect()
                    if (code > 0) {      // any HTTP response means the server is up
                        ready = true
                        return@repeat
                    }
                } catch (_: Exception) {
                    // Connection refused or timeout — server not ready yet
                }
                Thread.sleep(PROBE_INTERVAL_MS)
                handler.post {
                    txtStatus.text = "Waiting for server... (${attempt + 1}/$MAX_PROBE_ATTEMPTS)"
                }
            }

            handler.post {
                if (ready) {
                    txtStatus.text = ""
                    webView.loadUrl(url)
                } else {
                    txtStatus.text = "Could not connect. Restart the app."
                }
            }
        }.start()
    }

    private fun getCurrentPort(): Int {
        val prefs = getSharedPreferences(ThefeedService.PREFS_NAME, Context.MODE_PRIVATE)
        return prefs.getInt(ThefeedService.PREF_PORT, -1)
    }

    override fun onDestroy() {
        handler.removeCallbacksAndMessages(null)
        webView.destroy()
        super.onDestroy()
    }

    companion object {
        private const val MAX_PROBE_ATTEMPTS = 30
        private const val PROBE_INTERVAL_MS = 1000L   // 1s between probes → up to 30s total
        private const val PROBE_TIMEOUT_MS  = 1000L   // 1s HTTP connect timeout per probe
        private const val RETRY_DELAY_MS    = 2000L   // delay before restarting probe cycle on error
    }
}

