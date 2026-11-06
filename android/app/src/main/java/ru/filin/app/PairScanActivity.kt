package ru.filin.app

import android.Manifest
import android.app.Activity
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.drawable.GradientDrawable
import java.net.URLDecoder
import android.os.Bundle
import android.view.Gravity
import android.view.ViewGroup
import android.widget.FrameLayout
import android.widget.TextView
import androidx.activity.ComponentActivity
import androidx.activity.result.contract.ActivityResultContracts
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.core.content.ContextCompat
import com.google.mlkit.vision.barcode.BarcodeScannerOptions
import com.google.mlkit.vision.barcode.BarcodeScanning
import com.google.mlkit.vision.barcode.common.Barcode
import com.google.mlkit.vision.common.InputImage
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean

/** QR привязки с экрана «Телефоны» на компьютере: filin:pair?server=…&code=… */
data class PairCode(val server: String, val code: String) {
    companion object {
        const val SCHEME = "filin:pair"

        /** Разбор текста QR; чужой QR — null. Чистый Kotlin, без android.net.Uri — проверяется юнит-тестом. */
        fun parse(text: String): PairCode? {
            if (!text.startsWith("$SCHEME?")) return null
            val q = text.removePrefix("$SCHEME?").split('&').mapNotNull {
                val kv = it.split('=', limit = 2)
                if (kv.size == 2) kv[0] to URLDecoder.decode(kv[1], "UTF-8") else null
            }.toMap()
            val server = q["server"]?.trim()?.trimEnd('/') ?: return null
            val code = q["code"]?.trim() ?: return null
            if (!server.startsWith("http://") && !server.startsWith("https://")) return null
            if (!Regex("^\\d{6}$").matches(code)) return null
            return PairCode(server, code)
        }
    }
}

// Сканер QR привязки: камера → ML Kit (на телефоне, без сети) → адрес и код обратно в форму привязки.
class PairScanActivity : ComponentActivity() {
    private val done = AtomicBoolean(false)
    private val analyzer = Executors.newSingleThreadExecutor()
    private lateinit var status: TextView
    private lateinit var preview: PreviewView
    private var detector: com.google.mlkit.vision.barcode.BarcodeScanner? = null

    private val askCamera = registerForActivityResult(ActivityResultContracts.RequestPermission()) { ok ->
        if (ok) startCamera() else status.text = "Без камеры код не снять — разрешите её в настройках или введите адрес и код вручную."
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        preview = PreviewView(this)
        status = TextView(this).apply {
            text = "Наведите на QR-код на странице «Телефоны» компьютера"
            setTextColor(0xFFFFFFFF.toInt())
            textSize = 17f
            gravity = Gravity.CENTER
            setShadowLayer(4f, 0f, 1f, 0xFF000000.toInt())
            setPadding(dp(16), 0, dp(16), 0)
        }
        val frame = android.view.View(this).apply {
            background = GradientDrawable().apply {
                setStroke(dp(3), 0xD9FFFFFF.toInt())
                cornerRadius = dp(8).toFloat()
            }
        }
        val root = FrameLayout(this).apply {
            addView(preview, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
            addView(frame, FrameLayout.LayoutParams(dp(240), dp(240), Gravity.CENTER))
            addView(status, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT,
                Gravity.TOP).apply { topMargin = dp(48) })
        }
        fitSystemBars(root)
        setContentView(root)
        if (ContextCompat.checkSelfPermission(this, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED) {
            startCamera()
        } else {
            askCamera.launch(Manifest.permission.CAMERA)
        }
    }

    @androidx.annotation.OptIn(markerClass = [androidx.camera.core.ExperimentalGetImage::class])
    private fun startCamera() {
        val scanner = BarcodeScanning.getClient(
            BarcodeScannerOptions.Builder().setBarcodeFormats(Barcode.FORMAT_QR_CODE).build()
        )
        detector = scanner
        val future = ProcessCameraProvider.getInstance(this)
        future.addListener({
            val provider = future.get()
            val pv = Preview.Builder().build().also { it.setSurfaceProvider(preview.surfaceProvider) }
            val analysis = ImageAnalysis.Builder()
                .setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST)
                .build()
            analysis.setAnalyzer(analyzer) { proxy ->
                val media = proxy.image
                if (media == null || done.get()) {
                    proxy.close()
                    return@setAnalyzer
                }
                scanner.process(InputImage.fromMediaImage(media, proxy.imageInfo.rotationDegrees))
                    .addOnSuccessListener { codes ->
                        val v = codes.firstOrNull()?.rawValue ?: return@addOnSuccessListener
                        val pair = PairCode.parse(v)
                        if (pair == null) {
                            runOnUiThread { status.text = "Это не код Филина — нужен QR из раздела «2. Привязать телефон»" }
                        } else if (done.compareAndSet(false, true)) {
                            runOnUiThread {
                                setResult(Activity.RESULT_OK, Intent().putExtra(EXTRA_SERVER, pair.server).putExtra(EXTRA_CODE, pair.code))
                                finish()
                            }
                        }
                    }
                    .addOnCompleteListener { proxy.close() }
            }
            provider.unbindAll()
            provider.bindToLifecycle(this, CameraSelector.DEFAULT_BACK_CAMERA, pv, analysis)
        }, ContextCompat.getMainExecutor(this))
    }

    override fun onDestroy() {
        super.onDestroy()
        analyzer.shutdown()
        detector?.close()
    }

    companion object {
        const val EXTRA_SERVER = "server"
        const val EXTRA_CODE = "code"
    }
}
