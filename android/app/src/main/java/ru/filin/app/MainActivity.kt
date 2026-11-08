package ru.filin.app

import android.annotation.SuppressLint
import android.content.Intent
import android.graphics.drawable.GradientDrawable
import android.net.Uri
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.webkit.CookieManager
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Button
import android.widget.FrameLayout
import androidx.activity.ComponentActivity
import androidx.activity.OnBackPressedCallback
import androidx.activity.result.contract.ActivityResultContracts
import androidx.lifecycle.Lifecycle
import androidx.core.content.ContextCompat
import androidx.core.content.FileProvider
import android.Manifest
import android.app.AlertDialog
import android.content.pm.PackageManager
import java.io.File

// Главный экран: Филин целиком (каталог, карточки, выдача, ценность) — экраны веб-части,
// вход по куке привязанного телефона. Поверх — кнопка сканера.
class MainActivity : ComponentActivity() {
    private lateinit var web: WebView
    private var fileCallback: ValueCallback<Array<Uri>>? = null

    // фото обложки для форм веб-части (<input type=file>): с камеры или из галереи
    private val pickImage = registerForActivityResult(ActivityResultContracts.GetContent()) { uri ->
        fileCallback?.onReceiveValue(if (uri != null) arrayOf(uri) else null)
        fileCallback = null
    }
    private val shot by lazy { File(cacheDir, "web-cover.jpg") }
    private val takePhoto = registerForActivityResult(ActivityResultContracts.TakePicture()) { ok ->
        val uri = FileProvider.getUriForFile(this, "ru.filin.app.files", shot)
        fileCallback?.onReceiveValue(if (ok && shot.length() > 0) arrayOf(uri) else null)
        fileCallback = null
    }

    private fun shoot() {
        shot.delete()
        takePhoto.launch(FileProvider.getUriForFile(this, "ru.filin.app.files", shot))
    }

    private val askCamera = registerForActivityResult(ActivityResultContracts.RequestPermission()) { ok ->
        if (ok) {
            shoot()
        } else {
            fileCallback?.onReceiveValue(null)
            fileCallback = null
        }
    }

    private fun chooseImage() {
        AlertDialog.Builder(this)
            .setTitle("Фото обложки")
            .setItems(arrayOf("📷 Снять камерой", "🖼 Из галереи")) { _, which ->
                if (which == 0) {
                    // CAMERA объявлен в приложении (сканер) — без выданного разрешения системная камера не откроется
                    if (ContextCompat.checkSelfPermission(this, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED) {
                        shoot()
                    } else {
                        askCamera.launch(Manifest.permission.CAMERA)
                    }
                } else {
                    pickImage.launch("image/*")
                }
            }
            // закрыли окно, ничего не выбрав, — форма должна узнать об отказе, иначе поле больше не нажмётся
            .setOnCancelListener {
                fileCallback?.onReceiveValue(null)
                fileCallback = null
            }
            .show()
    }

    // после сканера каталог перечитывается: там новые книги. Только после сканера —
    // перечитывать на каждом возврате значило бы терять заполненную форму после выбора фото.
    private val scanner = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) {
        if (::web.isInitialized) web.reload()
    }

    // «Дополнить из РГБ» в карточке книги: поиск — своим браузером, как при скане; выбранная
    // запись открывает на сервере форму дополнения (пустые поля заполнены, своё не тронуто)
    private var fillBook: Long = 0
    private val pickRecord = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { res ->
        val record = res.data?.getStringExtra(RslActivity.EXTRA_RECORD) ?: return@registerForActivityResult
        if (fillBook > 0 && ::web.isInitialized) {
            web.loadUrl(Prefs.server(this) + "/book/$fillBook/edit?rsl=" + Uri.encode(record) + "#rsl")
        }
    }

    /** Ссылка «/book/{id}/rsl?q=…» — поиск в РГБ для заведённой книги; false — не она. */
    private fun rslLink(url: Uri): Boolean {
        val id = rslPath.matchEntire(url.path.orEmpty())?.groupValues?.get(1)?.toLongOrNull() ?: return false
        val q = url.getQueryParameter("q").orEmpty()
        if (q.isBlank()) return false // искать нечего — пусть сервер покажет форму со ссылкой
        fillBook = id
        pickRecord.launch(Intent(this, RslActivity::class.java).putExtra(RslActivity.EXTRA_QUERY, q)
            .putExtra(RslActivity.EXTRA_FALLBACK, url.getQueryParameter("fallback").orEmpty()))
        return true
    }

    private val rslPath = Regex("""/book/(\d+)/rsl""")

    // пока открыт поиск, Android может пересоздать этот экран (поворот, нехватка памяти):
    // номер книги переживает это, иначе выбранная запись пропала бы молча
    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        outState.putLong(STATE_FILL_BOOK, fillBook)
    }

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        fillBook = savedInstanceState?.getLong(STATE_FILL_BOOK) ?: 0
        if (!Prefs.paired(this)) {
            startActivity(Intent(this, SetupActivity::class.java))
            finish()
            return
        }
        val server = Prefs.server(this)
        val host = Uri.parse(server).host

        CookieManager.getInstance().apply {
            setAcceptCookie(true)
            setCookie(server, "filin_device=${Prefs.token(this@MainActivity)}; Path=/; HttpOnly")
            flush()
        }

        web = WebView(this).apply {
            settings.javaScriptEnabled = true // подсказки жанров и подтверждения в формах
            settings.domStorageEnabled = true
            setBackgroundColor(color(R.color.parchment))
            webViewClient = object : WebViewClient() {
                // свои страницы — внутри, чужие ссылки (Википедия, LiveLib) — в браузере
                override fun shouldOverrideUrlLoading(view: WebView, req: WebResourceRequest): Boolean {
                    if (req.url.host == host) return rslLink(req.url)
                    startActivity(Intent(Intent.ACTION_VIEW, req.url))
                    return true
                }

                // нет связи с компьютером — Филин закрыт или компьютер выключен: свой экран
                // вместо системной страницы ошибки
                override fun onReceivedError(view: WebView, req: WebResourceRequest, error: WebResourceError) {
                    if (req.isForMainFrame) {
                        failed = true
                        showOffline(true)
                    }
                }

                override fun onPageFinished(view: WebView, url: String) {
                    if (!failed) showOffline(false)
                }

                // доступ отозван на компьютере — заново привязать
                override fun onReceivedHttpError(view: WebView, req: WebResourceRequest, resp: WebResourceResponse) {
                    if (req.isForMainFrame && resp.statusCode == 401) {
                        Prefs.forget(this@MainActivity)
                        startActivity(Intent(this@MainActivity, SetupActivity::class.java))
                        finish()
                    }
                }
            }
            webChromeClient = object : WebChromeClient() {
                override fun onShowFileChooser(view: WebView, cb: ValueCallback<Array<Uri>>, params: FileChooserParams): Boolean {
                    fileCallback?.onReceiveValue(null)
                    fileCallback = cb
                    chooseImage()
                    return true
                }
            }
        }

        val scan = Button(this).apply {
            text = "📷"
            textSize = 26f
            contentDescription = "Сканировать штрихкод"
            background = GradientDrawable().apply {
                shape = GradientDrawable.OVAL
                setColor(color(R.color.accent))
            }
            elevation = dp(6).toFloat()
            setOnClickListener { scanner.launch(Intent(this@MainActivity, ScanActivity::class.java)) }
        }
        // Филин работает, пока он запущен на компьютере (решено 06.11.2026): без него — этот
        // экран, а не сканер: без компьютера книги не заводятся
        offline = column(24).apply {
            gravity = Gravity.CENTER
            setBackgroundColor(color(R.color.parchment))
            isClickable = true // поверх каталога и кнопки сканера: касания не уходят под экран
            visibility = View.GONE
            addView(text("🦉", size = 64f).apply { gravity = Gravity.CENTER })
            addView(title("Филин на компьютере не запущен").apply { gravity = Gravity.CENTER })
            addView(text("Запустите Филина ярлыком на компьютере — телефон увидит его сам, обычно за несколько секунд.\n\nЕсли Филин запущен: компьютер и телефон в одной сети? Адрес в приложении: $server",
                muted = true).apply { gravity = Gravity.CENTER })
            addView(button("Повторить") { retry() })
        }
        val root = FrameLayout(this).apply {
            addView(web, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
            addView(scan, FrameLayout.LayoutParams(dp(68), dp(68), Gravity.BOTTOM or Gravity.END).apply {
                setMargins(0, 0, dp(20), dp(24))
            })
            addView(offline, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
        }
        fitSystemBars(root)
        setContentView(root)

        onBackPressedDispatcher.addCallback(this, object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() {
                // под экраном «не запущен» история каталога не листается: «Назад» — выход
                if (offline.visibility != View.VISIBLE && web.canGoBack()) web.goBack() else finish()
            }
        })

        web.loadUrl(server + safePath(intent.getStringExtra(EXTRA_PATH)))
        FilinApp.showLastCrash(this)
        Updater.check(this)
    }

    // --- экран «Филин не запущен» ---

    private lateinit var offline: View
    private var failed = false // главная страница не загрузилась: её onPageFinished не должен прятать экран
    private val watch = Handler(Looper.getMainLooper())
    private var probing = false

    private fun showOffline(on: Boolean) {
        if (!::offline.isInitialized) return
        offline.visibility = if (on) View.VISIBLE else View.GONE
        watch.removeCallbacks(probe)
        if (on) watch.postDelayed(probe, PROBE_MS)
    }

    private fun retry() {
        failed = false
        web.reload()
    }

    // Пока экран открыт — раз в несколько секунд спросить компьютер. Любой ответ сервера
    // (даже 404 у Филина постарше без /api/ping) значит «Филин запущен» — тогда перечитать.
    private val probe: Runnable = Runnable {
        if (probing || offline.visibility != View.VISIBLE) return@Runnable
        probing = true
        background({ Api(this).call("GET", "/api/ping", waitMs = 3000) }) { r ->
            probing = false
            val alive = r.isSuccess || (r.exceptionOrNull() as? ServerError)?.code?.let { it > 0 } == true
            when {
                offline.visibility != View.VISIBLE -> {}
                alive -> retry()
                // ответ пришёл, когда приложение уже ушло в фон: не заводить опрос заново —
                // его продолжит onResume
                !lifecycle.currentState.isAtLeast(Lifecycle.State.RESUMED) -> {}
                else -> watch.postDelayed(probe, PROBE_MS)
            }
        }
    }

    override fun onPause() {
        super.onPause()
        watch.removeCallbacks(probe) // в фоне компьютер не опрашиваем
    }

    override fun onResume() {
        super.onResume()
        if (::offline.isInitialized && offline.visibility == View.VISIBLE) watch.postDelayed(probe, PROBE_MS)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        intent.getStringExtra(EXTRA_PATH)?.let { web.loadUrl(Prefs.server(this) + safePath(it)) }
    }

    companion object {
        const val EXTRA_PATH = "path"
        private const val STATE_FILL_BOOK = "fill_book"
        private const val PROBE_MS = 5000L

        /**
         * Путь внутри Филина из чужих рук: экран каталога открывается и другими приложениями
         * телефона. «@evil.com/» склеился бы в http://адрес:8080@evil.com/ — чужой сайт внутри
         * Филина. Пускаем только путь от корня самого Филина, иначе — каталог.
         */
        fun safePath(p: String?): String =
            if (p != null && p.startsWith("/") && !p.startsWith("//") && p.none { it == '@' || it == '\\' || it.isWhitespace() }) p else "/"
    }
}
