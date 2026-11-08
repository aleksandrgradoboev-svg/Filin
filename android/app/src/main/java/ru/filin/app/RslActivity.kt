package ru.filin.app

import android.annotation.SuppressLint
import android.app.Activity
import android.content.Intent
import android.graphics.Bitmap
import android.net.Uri
import android.net.http.SslError
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.View
import android.view.ViewGroup
import android.webkit.SslErrorHandler
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.ScrollView
import androidx.activity.ComponentActivity
import org.json.JSONArray
import org.json.JSONObject

// Поиск в РГБ делает человек — в браузере своего телефона: программам поиск РГБ закрыт
// (robots.txt и проверка браузерной сессии), а браузер человека — не программа.
// Сайт открывается скрыто, выдача читается со страницы и показывается списком:
// «Найдено: … → это она». Номер выбранной записи уходит на сервер Филина, запись
// читает он. Если список не сложился — можно открыть сам сайт или вставить ссылку из Chrome.
class RslActivity : ComponentActivity() {
    private val record = Regex("""(?:search\.rsl\.ru/(?:\w+/)?record/)?(\d{8,14})""")
    private val recordURL = Regex("""search\.rsl\.ru/(?:\w+/)?record/(\d{8,14})""")
    private val main = Handler(Looper.getMainLooper())
    private var polls = 0
    private var polling = false
    private var failed = false

    private lateinit var web: WebView
    private lateinit var status: android.widget.TextView
    private lateinit var err: android.widget.TextView
    private lateinit var list: LinearLayout
    private lateinit var resultsPane: ScrollView

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        attempts = attempts(
            intent.getStringExtra(EXTRA_QUERY).orEmpty(),
            intent.getStringExtra(EXTRA_FALLBACK).orEmpty(),
            intent.getIntExtra(EXTRA_YEAR, 0),
        )
        current = describe(attempts[0])
        val url = "https://search.rsl.ru/ru/search#" + fragment(attempts[0])

        status = text("Ищу в РГБ: $current", muted = true)
        err = errorText()
        list = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        val paste = field("или ссылка на запись РГБ из Chrome")
        val col = column().apply {
            setBackgroundColor(color(R.color.parchment))
            addView(title("Поиск в РГБ"))
            addView(status)
            addView(err)
            addView(list)
            addView(text("Нет нужного издания?", muted = true, size = 14f).apply { setPadding(0, dp(16), 0, 0) })
            addView(button("Показать сайт РГБ", primary = false) { resultsPane.visibility = View.GONE })
            addView(button("Открыть в Chrome", primary = false) { startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url))) })
            addView(paste)
            addView(button("Взять запись по ссылке", primary = false) {
                val id = record.find(paste.text.toString().trim())?.groupValues?.get(1)
                if (id == null) err.show("Это не ссылка на запись РГБ: нужен адрес вида search.rsl.ru/ru/record/01001234567")
                else done(id)
            })
        }
        resultsPane = ScrollView(this).apply { addView(col) }

        web = WebView(this).apply {
            settings.javaScriptEnabled = true // поиск РГБ без скриптов не работает
            settings.domStorageEnabled = true
            webViewClient = object : WebViewClient() {
                override fun shouldOverrideUrlLoading(view: WebView, req: WebResourceRequest) = picked(req.url.toString())

                // запись бывает открыта и без перехода по ссылке — ловим и начало загрузки
                override fun onPageStarted(view: WebView, url: String, favicon: Bitmap?) {
                    picked(url)
                }

                // бывает и несколько раз за загрузку — ждать выдачу хватит одного круга
                override fun onPageFinished(view: WebView, url: String) {
                    if (!failed && !polling) {
                        polling = true
                        poll()
                    }
                }

                // без этого сбой загрузки — просто белое поле
                override fun onReceivedSslError(view: WebView, handler: SslErrorHandler, error: SslError) {
                    if (RslTrust.trusted(this@RslActivity, error)) {
                        handler.proceed() // цепочка РГБ проверена вручную — см. RslTrust
                        return
                    }
                    handler.cancel() // любой другой недоверенный сертификат не пропускаем
                    fail("телефон не доверяет сертификату сайта (ошибка ${error.primaryError})")
                }

                override fun onReceivedError(view: WebView, req: WebResourceRequest, error: WebResourceError) {
                    if (req.isForMainFrame) fail("${error.description} (код ${error.errorCode})")
                }
            }
        }
        val root = FrameLayout(this).apply {
            addView(web, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
            addView(resultsPane, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
        }
        fitSystemBars(root)
        setContentView(root)
        web.loadUrl(url)
    }

    // Выдача приходит скриптом сайта уже после загрузки страницы — ждём её до 20 секунд.
    // После повторного поиска на странице ещё висит прежняя строка «Результатов: …» — её не считаем.
    private fun poll() {
        web.evaluateJavascript(READ_RESULTS) { raw ->
            val r = runCatching { JSONObject(JSONArray("[$raw]").getString(0)) }.getOrNull()
            when {
                r != null && r.optBoolean("done") && r.optString("info") != stale -> show(r)
                ++polls < 40 -> main.postDelayed({ poll() }, 500)
                else -> status.text = "РГБ не ответила за 20 секунд. Посмотрите сам сайт или откройте в Chrome."
            }
        }
    }

    private var current = ""
    private var stale: String? = null
    private var attempts: List<Pair<String, Int>> = emptyList()
    private var attempt = 0

    private fun show(r: JSONObject) {
        val items = r.getJSONArray("items")
        list.removeAllViews()
        if (items.length() == 0) {
            if (attempt + 1 < attempts.size) {
                // название могло распознаться с ошибкой («рен!» вместо «Солдат»), год — не тот:
                // запрос ослабляется по шагам, как на компьютере (web.rslAttempts)
                val was = current
                attempt++
                current = describe(attempts[attempt])
                status.text = "По «$was» ничего. Ищу: $current"
                stale = r.optString("info")
                polls = 0
                web.evaluateJavascript("location.hash=${JSONObject.quote("#" + fragment(attempts[attempt]))}", null)
                poll()
                return
            }
            status.text = "В РГБ ничего не нашлось по «$current». Поправьте название, автора или год в форме — или поищите на сайте."
            return
        }
        val note = if (attempt == 0) "" else " (по «$current» — проверьте название и год в форме)"
        status.text = r.optString("info").substringBefore(",").ifEmpty { "Найдено" } + note + ". Выберите своё издание:"
        for (i in 0 until items.length()) {
            val item = items.getJSONObject(i)
            val card = sheet()
            if (item.optString("author").isNotBlank()) card.addView(text(item.optString("author"), size = 15f))
            card.addView(text(item.optString("info"), size = 15f))
            card.addView(button("Это она") { done(item.getString("id")) })
            list.addView(card)
        }
    }

    private fun fail(why: String) {
        failed = true
        status.text = "РГБ не открылась: $why."
        err.show("Найдите запись в Chrome и вставьте ссылку на неё ниже.")
    }

    private fun picked(url: String): Boolean {
        val id = recordURL.find(url)?.groupValues?.get(1) ?: return false
        done(id)
        return true
    }

    private fun done(id: String) {
        setResult(Activity.RESULT_OK, Intent().putExtra(EXTRA_RECORD, id))
        finish()
    }

    override fun onDestroy() {
        main.removeCallbacksAndMessages(null)
        web.destroy()
        super.onDestroy()
    }

    companion object {
        const val EXTRA_QUERY = "query"
        const val EXTRA_FALLBACK = "fallback" // запрос, если по основному пусто (только автор)
        const val EXTRA_YEAR = "year" // год издания: фильтр «с … по …» поиска РГБ; 0 — без него

        /**
         * По чему искать, по порядку до первой непустой выдачи — как web.rslAttempts на сервере:
         * с годом (он сужает выдачу в десятки раз: «Оцеола» без года — 680 записей), автор с
         * годом, название и автор без года. Только автора без года не ищем. ISBN — один запрос;
         * год неправдоподобный — без него.
         */
        fun attempts(query: String, fallback: String, year: Int): List<Pair<String, Int>> {
            if (query.startsWith("isbn:")) return listOf(query to 0)
            val y = year.takeIf { it in 1700..(java.time.Year.now().value + 1) } ?: 0
            val out = mutableListOf<Pair<String, Int>>()
            if (y > 0) {
                if (query.isNotEmpty()) out += query to y
                if (fallback.isNotEmpty()) out += fallback to y
            }
            if (query.isNotEmpty()) out += query to 0
            // «только автор» без года — нет: у популярного автора сотни книг, в выдаче утонешь
            return out.distinct().ifEmpty { listOf(query to 0) }
        }

        /** Часть адреса поиска РГБ после «#»: год — фильтром yf/yt, как пишет его сама страница. */
        fun fragment(a: Pair<String, Int>): String =
            (if (a.second > 0) "yf=${a.second}&yt=${a.second}&" else "") + "q=" + Uri.encode(a.first)

        /**
         * Автор для запроса РГБ — как web.rslAuthor на сервере: все слова первого имени, кроме
         * инициалов; РГБ ищет запись, где есть каждое. Одно самое длинное слово искало
         * «Густав Эмар» как «Густав» — и находило Густава Малера.
         */
        fun authorQuery(names: String): String {
            val ws = names.split(",").first().split(Regex("""\s+"""))
                .map { it.trim('«', '»', '"', '(', ')', '[', ']') }
                .filter { it.length > 1 && !it.contains('.') }
            return when (ws.size) {
                0 -> ""
                1 -> "author:${ws[0]}"
                else -> "author:(${ws.joinToString(" ")})"
            }
        }

        /** Как показать попытку человеку: «title:(…) author:Рид, 1983 г.». */
        fun describe(a: Pair<String, Int>): String = a.first + if (a.second > 0) ", ${a.second} г." else ""
        const val EXTRA_RECORD = "record"

        // Читает выдачу со страницы поиска РГБ: номер записи, автор, описание издания.
        // done — поиск закончен (строка «Результатов: …» появилась), даже если ничего не нашлось.
        private const val READ_RESULTS = """(function(){
            var info=document.querySelector('.rsl-search-info');
            var items=[].slice.call(document.querySelectorAll('.search-item')).map(function(e){
                var m=e.querySelector('.js-item-maininfo'), a=e.querySelector('.js-item-authorinfo');
                return {id:m?m.getAttribute('data-id'):'', author:a?a.innerText.trim():'', info:m?m.innerText.trim():''};
            }).filter(function(x){return x.id});
            return JSON.stringify({done:!!info, info:info?info.innerText.trim():'', items:items});
        })()"""
    }
}
