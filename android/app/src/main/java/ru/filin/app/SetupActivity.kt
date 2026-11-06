package ru.filin.app

import android.app.Activity
import android.content.Intent
import android.os.Build
import android.os.Bundle
import android.widget.Button
import android.widget.EditText
import android.widget.ScrollView
import android.widget.TextView
import androidx.activity.ComponentActivity
import androidx.activity.result.contract.ActivityResultContracts
import org.json.JSONObject

// Привязка: адрес Филина в домашней сети и код со страницы «Телефоны» на компьютере.
// Проще всего — снять QR-код с той страницы: адрес и код подставятся сами.
class SetupActivity : ComponentActivity() {
    private lateinit var server: EditText
    private lateinit var code: EditText
    private lateinit var name: EditText
    private lateinit var err: TextView
    private lateinit var go: Button

    private val scanPair = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { r ->
        val data = r.data
        if (r.resultCode == Activity.RESULT_OK && data != null) {
            server.setText(data.getStringExtra(PairScanActivity.EXTRA_SERVER))
            code.setText(data.getStringExtra(PairScanActivity.EXTRA_CODE))
            pair() // всё уже есть — не заставлять нажимать ещё раз
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val col = column(20)
        server = field("Адрес Филина, например http://192.168.1.10:8080", Prefs.server(this).ifEmpty { "http://" })
        code = field("Код из шести цифр", digits = true)
        name = field("Название телефона", "${Build.MANUFACTURER} ${Build.MODEL}".trim())
        err = errorText()

        col.addView(title("Привязать телефон"))
        col.addView(text("1. На компьютере откройте Филин → «Телефоны» → «Привязать телефон — показать код».", muted = true))
        col.addView(text("2. Снимите QR-код с экрана компьютера — телефон привяжется сам.", muted = true))
        col.addView(button("📷 Сканировать код с компьютера") {
            scanPair.launch(Intent(this, PairScanActivity::class.java))
        })
        col.addView(text("Или впишите адрес Филина и код вручную. Код действует 10 минут.", muted = true))
        col.addView(server)
        col.addView(code)
        col.addView(name)
        col.addView(err)
        go = button("Привязать", primary = false) { pair() }
        col.addView(go)

        val scroll = ScrollView(this).apply { addView(col) }
        fitSystemBars(scroll)
        setContentView(scroll)
    }

    private fun pair() {
        val base = server.text.toString().trim().trimEnd('/')
        if (!base.startsWith("http://") && !base.startsWith("https://")) {
            err.show("адрес начинается с http://, например http://192.168.1.10:8080")
            return
        }
        go.isEnabled = false
        err.show(null)
        val body = JSONObject().put("code", code.text.toString().trim()).put("name", name.text.toString().trim())
        background({ Api(base, "").postJSON("/api/pair", body).getString("token") }) { r ->
            go.isEnabled = true
            r.onSuccess { token ->
                Prefs.savePairing(this, base, token)
                startActivity(Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_CLEAR_TASK or Intent.FLAG_ACTIVITY_NEW_TASK))
                finish()
            }.onFailure { err.show(it.message) }
        }
    }
}
