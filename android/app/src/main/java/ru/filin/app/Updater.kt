package ru.filin.app

import android.app.Activity
import android.app.AlertDialog
import android.content.Intent
import android.net.Uri
import android.provider.Settings
import android.widget.Toast
import androidx.core.content.FileProvider
import java.io.File

// Самообновление: новую версию приложения кладут на сервер Филина. При запуске приложение
// спрашивает /api/app; если там версия новее — предлагает обновиться, скачивает APK и просит
// Android поставить его поверх. Данные не трогаются: они на сервере.
// Поверх ставится только APK с той же подписью — ключ один на все версии.
object Updater {
    fun check(a: Activity) {
        background({ Api(a).getJSON("/api/app") }) { r ->
            val info = r.getOrNull() ?: return@background // нет сведений — молча: сервер старый или APK не выложен
            val code = info.optInt("version_code")
            if (code <= BuildConfig.VERSION_CODE || a.isFinishing) return@background
            AlertDialog.Builder(a)
                .setTitle("Есть обновление")
                .setMessage("Филин ${info.optString("version_name")} — сейчас стоит ${BuildConfig.VERSION_NAME}.")
                .setPositiveButton("Обновить") { _, _ -> download(a, info.optString("apk", "/app.apk")) }
                .setNegativeButton("Позже", null)
                .show()
        }
    }

    private fun download(a: Activity, path: String) {
        // разрешение «ставить приложения» Android даёт по одному разу на источник — сначала оно
        if (!a.packageManager.canRequestPackageInstalls()) {
            Toast.makeText(a, "Разрешите Филину устанавливать приложения и нажмите «Обновить» ещё раз", Toast.LENGTH_LONG).show()
            a.startActivity(Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:${a.packageName}")))
            return
        }
        Toast.makeText(a, "Скачиваю обновление…", Toast.LENGTH_SHORT).show()
        val apk = File(a.cacheDir, "filin-update.apk")
        background({ Api(a).download(path, apk) }) { r ->
            r.onSuccess {
                val uri = FileProvider.getUriForFile(a, "ru.filin.app.files", apk)
                a.startActivity(Intent(Intent.ACTION_VIEW).setDataAndType(uri, "application/vnd.android.package-archive")
                    .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION))
            }.onFailure { Toast.makeText(a, "Обновление не скачалось: ${it.message}", Toast.LENGTH_LONG).show() }
        }
    }
}
