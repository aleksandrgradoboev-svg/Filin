package ru.filin.app

import android.content.Intent
import android.graphics.BitmapFactory
import android.net.Uri
import android.os.Bundle
import android.view.View
import android.widget.ArrayAdapter
import android.widget.AutoCompleteTextView
import android.widget.CheckBox
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.content.FileProvider
import org.json.JSONArray
import org.json.JSONObject
import java.io.File

// После скана: есть ли книга с этим ISBN. Есть — «ещё экземпляр» или открыть карточку;
// нет — короткая карточка: название, автор, издательство, год, полка, фото обложки.
// Без ISBN (советские книги без штрихкода) — сразу короткая карточка, заполнить её
// можно с фото титула. Полное описание — потом, в карточке Филина.
class BookActivity : ComponentActivity() {
    private lateinit var isbn: String // пусто — книга без штрихкода
    private lateinit var col: LinearLayout
    private val photo by lazy { File(cacheDir, "cover.jpg") }
    private val titlePhoto by lazy { File(cacheDir, "title.jpg") }
    private var hasPhoto = false
    private lateinit var thumb: ImageView
    private var onTitle: () -> Unit = {}

    private val takeTitle = registerForActivityResult(ActivityResultContracts.TakePicture()) { ok ->
        if (ok && titlePhoto.length() > 0) onTitle()
    }

    private val takePhoto = registerForActivityResult(ActivityResultContracts.TakePicture()) { ok ->
        hasPhoto = ok && photo.length() > 0
        if (hasPhoto) {
            // миниатюра: картинку с камеры целиком в память не грузим
            val opts = BitmapFactory.Options().apply { inSampleSize = 8 }
            thumb.setImageBitmap(BitmapFactory.decodeFile(photo.path, opts))
            thumb.visibility = View.VISIBLE
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        isbn = intent.getStringExtra(EXTRA_ISBN) ?: ""
        photo.delete()
        titlePhoto.delete()
        col = column()
        val scroll = ScrollView(this).apply { addView(col) }
        fitSystemBars(scroll)
        setContentView(scroll)

        if (isbn.isEmpty()) {
            col.addView(title("Книга без штрихкода"))
            newBookForm()
            return
        }
        col.addView(title("ISBN $isbn"))
        val loading = text("Ищу в Филине…", muted = true)
        col.addView(loading)
        background({ Api(this).getJSON("/api/isbn/$isbn") }) { r ->
            col.removeView(loading)
            // и сбой сети, и сбой построения экрана — текстом на экране, а не пустотой
            r.mapCatching { res ->
                if (!res.optBoolean("valid", true)) {
                    col.addView(text("⚠️ Контрольная цифра не сходится — проверьте номер. Книгу всё равно можно завести.", size = 15f))
                }
                val books = res.getJSONArray("books")
                if (books.length() == 0) newBookForm() else showFound(books)
            }.onFailure { col.addView(errorText().apply { show("${it.javaClass.simpleName}: ${it.message}") }) }
        }
    }

    // --- книга уже есть ---

    private fun showFound(books: JSONArray) {
        col.addView(text("Уже есть в Филине:", size = 17f))
        for (i in 0 until books.length()) {
            val b = books.getJSONObject(i)
            val card = sheet()
            card.addView(text(b.getString("title"), size = 18f))
            val authors = b.getJSONArray("authors").let { a -> (0 until a.length()).joinToString(", ") { a.getString(it) } }
            val meta = listOf(authors, b.optString("publisher"), b.optInt("year").takeIf { it > 0 }?.toString() ?: "")
                .filter { it.isNotBlank() }.joinToString(" · ")
            if (meta.isNotEmpty()) card.addView(text(meta, muted = true))
            card.addView(text("Экземпляров: ${b.getInt("copies")}", muted = true))
            val shelf = shelfFields(card)
            card.addView(button("Это она — ещё экземпляр на эту полку") {
                val body = JSONObject().put("room", shelf[0].text.toString()).put("bookcase", shelf[1].text.toString())
                    .put("shelf", shelf[2].text.toString())
                rememberShelf(shelf)
                send({
                    Api(this).postJSON("/api/books/${b.getLong("id")}/copies", body)
                    "Экземпляр добавлен"
                })
            })
            card.addView(button("Открыть карточку", primary = false) {
                startActivity(Intent(this, MainActivity::class.java).putExtra(MainActivity.EXTRA_PATH, b.getString("url"))
                    .addFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_SINGLE_TOP))
                finish()
            })
            col.addView(card)
        }
        col.addView(text("Издательства используют один ISBN повторно — если это другое издание, заведите его:", muted = true))
        col.addView(button("Другое издание — завести", primary = false) { newBookForm() })
    }

    // --- новой книги нет ---

    private fun newBookForm() {
        val card = sheet()
        card.addView(text("Новая книга", size = 18f))
        val fromRsl = text("", muted = true, size = 14f).apply { visibility = View.GONE }
        val title = field("Название *")
        val authors = field("Автор (несколько — через запятую)")
        val publisher = field("Издательство")
        val city = field("Город")
        val year = field("Год", digits = true)
        val rslErr = errorText()
        // «Снять страницу»: титул или каталожную карточку. Сервер распознаёт фото, сам решает,
        // что это, и раскладывает по полям — черновик, его проверяют. Не вошедшее в форму
        // (участники, ББК, аннотация) сервер возьмёт из той же пробы при сохранении.
        val fromTitle = text("", muted = true, size = 14f).apply { visibility = View.GONE }
        card.addView(button("📖 Снять титул или карточку", primary = isbn.isEmpty()) {
            onTitle = {
                rslErr.show(null)
                ocrSample = null // прежняя проба больше не про эту форму, даже если новая не удастся
                fromTitle.show("Распознаю страницу…")
                background({
                    JSONObject(Api(this).call("POST", "/api/title", titlePhoto.readBytes(), "image/jpeg", waitMs = 120000))
                }) { r ->
                    r.onSuccess { d -> showDraft(d, title, authors, publisher, city, year, fromTitle) }
                        .onFailure {
                            fromTitle.show(null)
                            rslErr.show("Страница: ${it.message}")
                        }
                }
            }
            takeTitle.launch(FileProvider.getUriForFile(this, "ru.filin.app.files", titlePhoto))
        })
        card.addView(fromTitle)
        // «Найти в РГБ»: ищет человек, на сайте РГБ; выбранную запись читает сервер Филина
        card.addView(button("🔎 Найти в РГБ", primary = false) {
            onRecord = { id ->
                rslErr.show(null)
                fromRsl.show("Читаю запись РГБ…")
                background({ Api(this).getJSON("/api/rsl/$id") }) { r ->
                    r.onSuccess { p ->
                        rslRecord = id
                        title.setText(p.optString("title"))
                        val a = p.optJSONArray("authors")
                        authors.setText(if (a == null) "" else (0 until a.length()).joinToString(", ") { a.getString(it) })
                        publisher.setText(p.optString("publisher"))
                        city.setText(p.optString("city"))
                        year.setText(p.optInt("year").takeIf { it > 0 }?.toString() ?: "")
                        val more = listOf(
                            p.optString("city"),
                            p.optInt("pages").takeIf { it > 0 }?.let { "$it с." },
                            p.optString("series").takeIf { it.isNotEmpty() }?.let { "серия «$it»" },
                            p.optInt("circulation").takeIf { it > 0 }?.let { "тираж $it" },
                        ).filter { !it.isNullOrBlank() }.joinToString(" · ")
                        fromRsl.show("✅ Из РГБ. Остальное (переводчики, страницы, серия, тираж, ББК) сохранится само" +
                            if (more.isEmpty()) "." else ": $more")
                    }.onFailure {
                        fromRsl.show(null)
                        rslErr.show("РГБ: ${it.message}")
                    }
                }
            }
            // без ISBN — ищем по тому, что уже в полях (например, с титула). Название — до двоеточия
            // и без кавычек: подзаголовок карточки сужает поиск сильнее, чем помогает. Автор — фамилия:
            // самое длинное слово без точки («Хастингс М.», «М. Риджуэй» → фамилия, а не инициал)
            val words = title.text.toString().substringBefore(":").replace(Regex("""[«»"„“()\[\]!?]"""), " ")
                .split(Regex("""\s+""")).filter { it.isNotBlank() }
            val byAuthor = RslActivity.authorQuery(authors.text.toString())
            val q = if (isbn.isNotEmpty()) "isbn:$isbn" else listOfNotNull(
                words.takeIf { it.isNotEmpty() }?.let { "title:(${it.joinToString(" ")})" },
                byAuthor.ifEmpty { null },
            ).joinToString(" ")
            if (q.isBlank()) {
                rslErr.show("Искать нечего: сначала снимите страницу или впишите название")
                return@button
            }
            // по названию и автору пусто — распознавание могло испортить название; тогда всё по автору
            val fallback = if (isbn.isEmpty() && words.isNotEmpty() && byAuthor.isNotEmpty()) byAuthor else ""
            pickRecord.launch(Intent(this, RslActivity::class.java).putExtra(RslActivity.EXTRA_QUERY, q)
                .putExtra(RslActivity.EXTRA_FALLBACK, fallback)
                // год с титула или из формы — самый сильный фильтр поиска (издательство в РГБ
                // записано как попало, а год печатается цифрами и читается надёжно)
                .putExtra(RslActivity.EXTRA_YEAR, year.text.toString().trim().toIntOrNull() ?: 0))
        })
        card.addView(fromRsl)
        card.addView(rslErr)
        listOf(title, authors, publisher, city, year).forEach { card.addView(it) }

        val atHome = CheckBox(this).apply {
            text = "Стоит у меня — завести экземпляр на полке"
            isChecked = true
            setTextColor(color(R.color.ink))
        }
        card.addView(atHome)
        val shelf = shelfFields(card)

        thumb = ImageView(this).apply {
            visibility = View.GONE
            adjustViewBounds = true
            maxHeight = dp(220)
        }
        card.addView(button("📷 Фото обложки", primary = false) {
            val uri: Uri = FileProvider.getUriForFile(this, "ru.filin.app.files", photo)
            takePhoto.launch(uri)
        })
        card.addView(thumb)
        val err = errorText()
        card.addView(err)
        card.addView(button("Сохранить") {
            if (title.text.isBlank()) {
                err.show("нужно название")
                return@button
            }
            val body = JSONObject()
                .put("title", title.text.toString().trim())
                .put("authors", JSONArray().put(authors.text.toString()))
                .put("publisher", publisher.text.toString().trim())
                .put("city", city.text.toString().trim())
                .put("ocr", ocrSample ?: "")
                .put("year", year.text.toString().toIntOrNull() ?: 0)
                .put("isbn", isbn)
                .put("with_copy", atHome.isChecked)
                .put("room", shelf[0].text.toString()).put("bookcase", shelf[1].text.toString())
                .put("shelf", shelf[2].text.toString())
                .put("rsl", rslRecord ?: "")
            if (atHome.isChecked) rememberShelf(shelf)
            val withPhoto = hasPhoto
            send({
                val api = Api(this)
                val id = api.postJSON("/api/books", body).getLong("id")
                // книга уже заведена: сбой фото — не «не сохранилось», иначе повторное
                // «Сохранить» заведёт её второй раз. Фото можно добавить в карточке.
                if (withPhoto) runCatching { api.call("PUT", "/api/books/$id/cover", photo.readBytes(), "image/jpeg") }
                    .exceptionOrNull()?.let { return@send "Книга заведена, но фото не загрузилось (${it.message}) — добавьте его в карточке" }
                "Книга заведена"
            })
        })
        col.addView(card)
        title.requestFocus()
    }

    /** Черновик со страницы — в поля формы, остальное — строкой «сохранится само». */
    private fun showDraft(
        d: JSONObject, title: android.widget.EditText, authors: android.widget.EditText, publisher: android.widget.EditText,
        city: android.widget.EditText, year: android.widget.EditText, info: android.widget.TextView,
    ) {
        ocrSample = d.optString("sample").ifEmpty { null }
        val isCard = d.optString("kind") == "card"
        fun fill(f: android.widget.EditText, v: String) { if (v.isNotBlank()) f.setText(v) }
        val sub = d.optString("subtitle")
        // в карточке подзаголовок — часть названия («Операция «Оверлорд»: Как был открыт…»),
        // на титуле это обычно жанр («роман») — его только показываем
        fill(title, if (isCard && sub.isNotBlank()) d.optString("title") + ": " + sub else d.optString("title"))
        val a = d.optJSONArray("authors")
        if (a != null && a.length() > 0) authors.setText((0 until a.length()).joinToString(", ") { a.getString(it) })
        fill(publisher, d.optString("publisher"))
        fill(city, d.optString("city"))
        d.optInt("year").takeIf { it > 0 }?.let { year.setText(it.toString()) }

        val roles = mapOf("translator" to "перевод", "editor" to "редактор", "compiler" to "составитель", "illustrator" to "художник")
        val people = d.optJSONArray("people")?.let { p ->
            (0 until p.length()).map { p.getJSONObject(it) }.joinToString(", ") { "${roles[it.optString("role")] ?: it.optString("role")} ${it.optString("name")}" }
        }.orEmpty()
        val isbns = d.optJSONArray("isbn")?.let { i -> (0 until i.length()).map { i.getString(it) } }.orEmpty()
        val more = listOfNotNull(
            people.ifEmpty { null },
            isbns.firstOrNull()?.let { "ISBN $it" },
            d.optInt("pages").takeIf { it > 0 }?.let { "$it с." },
            d.optString("bbk").ifEmpty { null }?.let { "ББК $it" },
            d.optString("orig_language").ifEmpty { null }?.let { "с языка: $it" },
            if (d.optString("annotation").isNotEmpty()) "аннотация" else null,
        ).joinToString(" · ")
        val others = d.optJSONArray("others")?.let { o -> (0 until o.length()).joinToString("; ") { o.getString(it) } }.orEmpty()
        info.show(buildString {
            append(if (isCard) "Распознал как: каталожная карточка" else "Распознал как: титул")
            if (d.optString("title").isBlank()) append("\n⚠️ Название не распознано — снимите ровнее и ближе или впишите руками")
            else append("\n✅ Проверьте поля: распознавание ошибается в буквах, имена собственные в названии с маленькой буквы")
            if (!isCard && sub.isNotBlank()) append("\nПодзаголовок: $sub")
            if (more.isNotEmpty()) append("\nСохранится само: $more")
            if (others.isNotEmpty()) append("\nЕщё на странице: $others")
        })
        // книга без штрихкода, а ISBN на странице есть — вдруг она уже в Филине
        val n = isbns.firstOrNull()
        if (isbn.isEmpty() && n != null) {
            background({ Api(this).getJSON("/api/isbn/" + Uri.encode(n)) }) { r ->
                r.onSuccess { res ->
                    val books = res.optJSONArray("books")
                    if (books != null && books.length() > 0) {
                        info.show(info.text.toString() + "\n⚠️ С этим ISBN уже есть: " + (0 until books.length())
                            .joinToString("; ") { books.getJSONObject(it).optString("title") } + " — может, это ещё экземпляр?")
                    }
                }
            }
        }
    }

    // --- общее ---

    /** Комната, шкаф, полка — с прошлого раза и с подсказками из Филина. */
    private fun shelfFields(into: LinearLayout): List<AutoCompleteTextView> {
        val (room, bookcase, shelf) = Prefs.lastShelf(this)
        val fields = listOf("Комната" to room, "Шкаф" to bookcase, "Полка" to shelf).map { (hint, v) ->
            AutoCompleteTextView(this).apply {
                val f = field(hint, v)
                this.hint = hint
                setText(v)
                background = f.background
                setPadding(dp(12), dp(10), dp(12), dp(10))
                setTextColor(color(R.color.ink))
                setHintTextColor(color(R.color.muted))
                threshold = 1
                layoutParams = f.layoutParams
            }
        }
        val row = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        fields.forEach { row.addView(it) }
        into.addView(row)
        background({ Api(this).getArray("/api/shelves") }) { r ->
            r.onSuccess { arr ->
                val keys = listOf("room", "bookcase", "shelf")
                fields.forEachIndexed { i, f ->
                    val vals = (0 until arr.length()).map { arr.getJSONObject(it).optString(keys[i]) }.filter { it.isNotBlank() }.distinct()
                    f.setAdapter(ArrayAdapter(this, android.R.layout.simple_dropdown_item_1line, vals))
                }
            }
        }
        return fields
    }

    private fun rememberShelf(f: List<AutoCompleteTextView>) =
        Prefs.saveShelf(this, f[0].text.toString().trim(), f[1].text.toString().trim(), f[2].text.toString().trim())

    /** Отправить и, если удалось, вернуться к сканеру — за следующей книгой. */
    /** work возвращает итог для человека; исключение — значит, на сервер не попало ничего. */
    private fun send(work: () -> String) {
        if (sending) return // второе нажатие, пока идёт первое, завело бы книгу дважды
        sending = true
        background(work) { r ->
            r.onSuccess { done ->
                Toast.makeText(this, "✅ $done", Toast.LENGTH_LONG).show()
                finish()
            }.onFailure {
                sending = false
                Toast.makeText(this, "Не сохранилось: ${it.message}", Toast.LENGTH_LONG).show()
            }
        }
    }

    private var sending = false

    // запись РГБ, выбранная человеком: сервер возьмёт из неё всю карточку
    private var rslRecord: String? = null
    private var ocrSample: String? = null // проба распознавания титула: сервер допишет к ней итог
    private var onRecord: (String) -> Unit = {}
    private val pickRecord = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { res ->
        res.data?.getStringExtra(RslActivity.EXTRA_RECORD)?.let { onRecord(it) }
    }

    companion object {
        const val EXTRA_ISBN = "isbn"
    }
}
