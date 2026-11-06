package ru.filin.app

import android.content.Context
import android.os.Handler
import android.os.Looper
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.net.HttpURLConnection
import java.net.URL
import java.util.concurrent.Executors

// Связь с сервером Филина: адрес и токен привязки хранятся в настройках приложения.
object Prefs {
    private const val FILE = "filin"

    private fun p(ctx: Context) = ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE)

    fun server(ctx: Context): String = p(ctx).getString("server", "") ?: ""
    fun token(ctx: Context): String = p(ctx).getString("token", "") ?: ""
    fun paired(ctx: Context) = server(ctx).isNotEmpty() && token(ctx).isNotEmpty()

    fun savePairing(ctx: Context, server: String, token: String) =
        p(ctx).edit().putString("server", server).putString("token", token).apply()

    fun forget(ctx: Context) = p(ctx).edit().remove("token").apply()

    // Последняя полка: книги сканируют подряд с одной полки — не вводить её каждый раз.
    fun lastShelf(ctx: Context): Triple<String, String, String> = p(ctx).let {
        Triple(it.getString("room", "") ?: "", it.getString("bookcase", "") ?: "", it.getString("shelf", "") ?: "")
    }

    fun saveShelf(ctx: Context, room: String, bookcase: String, shelf: String) =
        p(ctx).edit().putString("room", room).putString("bookcase", bookcase).putString("shelf", shelf).apply()
}

/** Ошибка сервера с текстом для человека (поле error ответа) или сетевая. */
class ServerError(val code: Int, message: String) : Exception(message)

class Api(private val base: String, private val token: String) {
    constructor(ctx: Context) : this(Prefs.server(ctx), Prefs.token(ctx))

    fun url(path: String) = base.trimEnd('/') + path

    /** Запрос к серверу; тело ответа — текстом. Ответ не 2xx — ServerError с текстом сервера. */
    fun call(method: String, path: String, body: ByteArray? = null, type: String? = null, waitMs: Int = 30000): String {
        val c = URL(url(path)).openConnection() as HttpURLConnection
        try {
            c.requestMethod = method
            c.connectTimeout = 7000
            c.readTimeout = waitMs
            if (token.isNotEmpty()) c.setRequestProperty("Authorization", "Bearer $token")
            if (body != null) {
                c.doOutput = true
                c.setRequestProperty("Content-Type", type ?: "application/json; charset=utf-8")
                c.outputStream.use { it.write(body) }
            }
            val code = c.responseCode
            val text = (if (code in 200..299) c.inputStream else c.errorStream)?.use { it.readBytes().decodeToString() } ?: ""
            if (code !in 200..299) {
                val msg = runCatching { JSONObject(text).getString("error") }.getOrNull()
                    ?: text.lineSequence().firstOrNull { it.isNotBlank() }?.take(200)
                    ?: "ошибка сервера $code"
                throw ServerError(code, msg)
            }
            return text
        } catch (e: java.io.IOException) {
            throw ServerError(0, "нет связи с Филином: ${e.message ?: "сервер не отвечает"}. Компьютер включён и в той же сети?")
        } finally {
            c.disconnect()
        }
    }

    fun getJSON(path: String) = JSONObject(call("GET", path))
    fun getArray(path: String) = JSONArray(call("GET", path))
    fun postJSON(path: String, body: JSONObject) = JSONObject(call("POST", path, body.toString().toByteArray()))

    /** Скачивает файл (обновление приложения) в dest. */
    fun download(path: String, dest: File) {
        val c = URL(url(path)).openConnection() as HttpURLConnection
        try {
            c.connectTimeout = 7000
            c.readTimeout = 60000
            if (c.responseCode != 200) throw ServerError(c.responseCode, "не скачалось: ответ ${c.responseCode}")
            c.inputStream.use { inp -> dest.outputStream().use { inp.copyTo(it) } }
        } finally {
            c.disconnect()
        }
    }
}

// Сеть — в фоне, результат — на экран. Простая пара вместо библиотек.
private val pool = Executors.newFixedThreadPool(3)
private val main = Handler(Looper.getMainLooper())

fun <T> background(work: () -> T, done: (Result<T>) -> Unit) {
    pool.execute {
        val r = runCatching(work)
        main.post { done(r) }
    }
}
