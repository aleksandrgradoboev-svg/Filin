package ru.filin.app

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

// Договор с сервером: текст QR делает web.pairText (Go), разбирает PairCode.parse.
class PairCodeTest {
    @Test
    fun parsesServerQr() {
        // ровно то, что выдаёт pairText("http://192.168.1.10:8080", "724048") — порядок ключей по алфавиту
        val p = PairCode.parse("filin:pair?code=724048&server=http%3A%2F%2F192.168.1.10%3A8080")
        assertEquals(PairCode("http://192.168.1.10:8080", "724048"), p)
    }

    @Test
    fun anyOrderAndTrailingSlash() {
        assertEquals(PairCode("http://10.0.0.5:8091", "000123"),
            PairCode.parse("filin:pair?server=http%3A%2F%2F10.0.0.5%3A8091%2F&code=000123"))
    }

    @Test
    fun refusesForeignCodes() {
        for (s in listOf(
            "http://192.168.1.10:8080/app.apk",                         // QR установки, не привязки
            "filin:pair?server=http%3A%2F%2Fx",                          // нет кода
            "filin:pair?code=724048",                                    // нет адреса
            "filin:pair?code=72404&server=http%3A%2F%2Fx",               // код не из шести цифр
            "filin:pair?code=724048&server=ftp%3A%2F%2Fx",               // не http
            "9785389209411",                                             // ISBN
        )) assertNull(s, PairCode.parse(s))
    }
}
