package ru.filin.app

import org.junit.Assert.assertEquals
import org.junit.Test

// Экран каталога открывают и чужие приложения: путь не должен уводить с адреса Филина.
class SafePathTest {
    @Test
    fun keepsOwnPaths() {
        for (p in listOf("/", "/book/21", "/add?isbn=9785389209411", "/?q=%D0%B0"))
            assertEquals(p, MainActivity.safePath(p))
    }

    @Test
    fun refusesForeign() {
        for (p in listOf(null, "", "@evil.com/", "//evil.com/", "evil.com", "/\\evil.com", "/ a", "http://evil.com/"))
            assertEquals(p.toString(), "/", MainActivity.safePath(p))
    }
}
