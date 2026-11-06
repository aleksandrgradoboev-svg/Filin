package ru.filin.app

import android.app.Activity
import android.app.AlertDialog
import android.app.Application
import java.io.File

// Падение приложения не должно пропадать молча: отладки по кабелю на телефоне нет,
// поэтому текст ошибки пишется в файл и показывается при следующем запуске.
class FilinApp : Application() {
    override fun onCreate() {
        super.onCreate()
        val prev = Thread.getDefaultUncaughtExceptionHandler()
        Thread.setDefaultUncaughtExceptionHandler { t, e ->
            runCatching { crashFile(this).writeText("Филин ${BuildConfig.VERSION_NAME}\n" + e.stackTraceToString()) }
            prev?.uncaughtException(t, e)
        }
    }

    companion object {
        fun crashFile(app: android.content.Context) = File(app.filesDir, "crash.txt")

        /** Показывает прошлое падение, если было, и забывает его. */
        fun showLastCrash(a: Activity) {
            val f = crashFile(a)
            if (!f.exists()) return
            val text = f.readText().take(3000)
            f.delete()
            AlertDialog.Builder(a)
                .setTitle("Филин в прошлый раз упал")
                .setMessage("Перешлите этот текст — по нему ищется ошибка:\n\n$text")
                .setPositiveButton("Понятно", null)
                .show()
                .findViewById<android.widget.TextView>(android.R.id.message)?.setTextIsSelectable(true)
        }
    }
}
