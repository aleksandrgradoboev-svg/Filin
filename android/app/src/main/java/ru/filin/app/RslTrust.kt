package ru.filin.app

import android.content.Context
import android.net.Uri
import android.net.http.SslError
import android.os.Build
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate

// Сертификат РГБ выдан от корня GlobalSign Root R46 (2019), которого нет в старых Android:
// WebView отказывает с «недоверенным сертификатом». Если network_security_config до WebView
// не доходит, проверяем цепочку сами: сертификат сайта подписан промежуточным GlobalSign
// GCC R46 DV TLS CA 2025, тот — корнем R46 (оба лежат в приложении, взяты у GlobalSign),
// сроки действуют, имя совпадает. Всё сошлось — продолжаем, иначе отказ.
// Только для адресов rsl.ru; любую другую ошибку сертификата это не пропускает.
object RslTrust {
    private fun load(ctx: Context, id: Int): X509Certificate =
        ctx.resources.openRawResource(id).use { CertificateFactory.getInstance("X.509").generateCertificate(it) as X509Certificate }

    fun trusted(ctx: Context, error: SslError): Boolean {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) return false // сертификат целиком отдаётся с Android 10
        val host = Uri.parse(error.url).host ?: return false
        if (host != "rsl.ru" && !host.endsWith(".rsl.ru")) return false
        if (error.primaryError != SslError.SSL_UNTRUSTED) return false // истёкший, чужое имя — не наш случай
        val leaf = error.certificate.x509Certificate ?: return false
        return runCatching {
            val root = load(ctx, R.raw.globalsign_root_r46)
            val inter = load(ctx, R.raw.globalsign_gcc_r46_dv_2025)
            root.checkValidity()
            inter.checkValidity()
            leaf.checkValidity()
            inter.verify(root.publicKey)
            leaf.verify(inter.publicKey)
            nameMatches(leaf, host)
        }.getOrDefault(false)
    }

    // *.rsl.ru подходит для search.rsl.ru, но не для a.b.rsl.ru — как у браузеров
    private fun nameMatches(cert: X509Certificate, host: String): Boolean =
        cert.subjectAlternativeNames.orEmpty().filter { it[0] == 2 }.map { (it[1] as String).lowercase() }.any { name ->
            name == host || name.startsWith("*.") && host.endsWith(name.substring(1)) &&
                !host.removeSuffix(name.substring(1)).contains('.')
        }
}
