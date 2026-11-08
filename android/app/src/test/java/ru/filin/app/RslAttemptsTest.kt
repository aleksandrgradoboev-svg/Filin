package ru.filin.app

import org.junit.Assert.assertEquals
import org.junit.Test

// Порядок поиска в РГБ — тот же, что у сервера (web.rslAttempts, TestRSLAttempts).
class RslAttemptsTest {
    private val q = "title:(Оцеола, вождь семинолов) author:Рид"
    private val fb = "author:Рид"

    @Test fun withYear() {
        assertEquals(listOf(q to 1983, fb to 1983, q to 0, fb to 0), RslActivity.attempts(q, fb, 1983))
    }

    @Test fun withoutYear() {
        assertEquals(listOf(q to 0, fb to 0), RslActivity.attempts(q, fb, 0))
        assertEquals(listOf(q to 0), RslActivity.attempts(q, "", 0))
    }

    // распознавание прочитало «1983» как «7983» — фильтра нет
    @Test fun implausibleYear() {
        assertEquals(listOf(q to 0, fb to 0), RslActivity.attempts(q, fb, 7983))
    }

    @Test fun isbnOnce() {
        assertEquals(listOf("isbn:9785042431180" to 0), RslActivity.attempts("isbn:9785042431180", "", 1983))
    }

    @Test fun describe() {
        assertEquals("$fb, 1983 г.", RslActivity.describe(fb to 1983))
        assertEquals(fb, RslActivity.describe(fb to 0))
    }
}
