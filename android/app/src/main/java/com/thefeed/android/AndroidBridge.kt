package com.thefeed.android

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.util.Base64
import android.webkit.JavascriptInterface
import androidx.core.content.pm.ShortcutInfoCompat
import androidx.core.content.pm.ShortcutManagerCompat
import androidx.core.graphics.drawable.IconCompat
import java.io.File
import java.io.FileOutputStream
import java.security.MessageDigest

class AndroidBridge(private val activity: Activity) {

    private val prefs by lazy {
        activity.getSharedPreferences(ThefeedService.PREFS_NAME, Context.MODE_PRIVATE)
    }

    // ===== Identity =====

    @JavascriptInterface
    fun isAndroid(): Boolean = true

    /**
     * Create a pinned shortcut on the home screen with a custom name and icon.
     * @param name The label shown under the shortcut
     * @param iconBase64 Base64-encoded image data (may include data:image/...;base64, prefix)
     */
    @JavascriptInterface
    fun createAppShortcut(name: String, iconBase64: String): Boolean {
        return try {
            val raw = if (iconBase64.contains(",")) {
                iconBase64.substringAfter(",")
            } else {
                iconBase64
            }
            val bytes = Base64.decode(raw, Base64.DEFAULT)
            val bitmap = BitmapFactory.decodeByteArray(bytes, 0, bytes.size) ?: return false

            // Save icon to internal storage for persistence
            val iconFile = File(activity.filesDir, "custom_shortcut_icon.png")
            FileOutputStream(iconFile).use { out ->
                bitmap.compress(Bitmap.CompressFormat.PNG, 100, out)
            }
            prefs.edit()
                .putString(PREF_CUSTOM_APP_NAME, name)
                .putString(PREF_CUSTOM_ICON_PATH, iconFile.absolutePath)
                .apply()

            val icon = IconCompat.createWithBitmap(bitmap)
            val shortcut = ShortcutInfoCompat.Builder(activity, "custom_launcher")
                .setShortLabel(name)
                .setLongLabel(name)
                .setIcon(icon)
                .setIntent(
                    Intent(activity, MainActivity::class.java).apply {
                        action = Intent.ACTION_MAIN
                    }
                )
                .build()
            ShortcutManagerCompat.requestPinShortcut(activity, shortcut, null)
            true
        } catch (_: Exception) {
            false
        }
    }

    @JavascriptInterface
    fun getCustomAppName(): String {
        return prefs.getString(PREF_CUSTOM_APP_NAME, "") ?: ""
    }

    @JavascriptInterface
    fun resetAppShortcut() {
        prefs.edit()
            .remove(PREF_CUSTOM_APP_NAME)
            .remove(PREF_CUSTOM_ICON_PATH)
            .apply()
        val iconFile = File(activity.filesDir, "custom_shortcut_icon.png")
        if (iconFile.exists()) iconFile.delete()
    }

    // ===== Password =====

    @JavascriptInterface
    fun hasPassword(): Boolean {
        return prefs.getString(PREF_PASSWORD_HASH, null) != null
    }

    @JavascriptInterface
    fun setPassword(password: String): Boolean {
        if (password.isEmpty()) return false
        prefs.edit().putString(PREF_PASSWORD_HASH, sha256(password)).apply()
        return true
    }

    @JavascriptInterface
    fun removePassword(currentPassword: String): Boolean {
        val stored = prefs.getString(PREF_PASSWORD_HASH, null) ?: return false
        if (sha256(currentPassword) != stored) return false
        prefs.edit().remove(PREF_PASSWORD_HASH).apply()
        return true
    }

    @JavascriptInterface
    fun checkPassword(password: String): Boolean {
        val stored = prefs.getString(PREF_PASSWORD_HASH, null) ?: return true // no password set
        return sha256(password) == stored
    }

    private fun sha256(input: String): String {
        val digest = MessageDigest.getInstance("SHA-256")
        val hash = digest.digest(input.toByteArray(Charsets.UTF_8))
        return hash.joinToString("") { "%02x".format(it) }
    }

    companion object {
        const val PREF_CUSTOM_APP_NAME = "custom_app_name"
        const val PREF_CUSTOM_ICON_PATH = "custom_icon_path"
        const val PREF_PASSWORD_HASH = "password_hash"
    }
}
