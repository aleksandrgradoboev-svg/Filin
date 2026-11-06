import java.util.Properties

plugins {
    id("com.android.application")
}

// Ключ подписи выпуска — вне репозитория. Путь к файлу с ним — в local.properties
// (filin.signing=…), сам local.properties в git не попадает. Ключ один на все версии:
// обновление ставится поверх, только если подпись та же.
fun props(f: File) = Properties().apply { if (f.exists()) f.inputStream().use { load(it) } }
val signing: Properties? = props(rootProject.file("local.properties")).getProperty("filin.signing")
    ?.let { file(it) }?.takeIf { it.exists() }?.let { props(it) }

android {
    namespace = "ru.filin.app"
    compileSdk = 37 // androidx.core 1.19 собирается только против 37; поведение задаёт targetSdk

    defaultConfig {
        applicationId = "ru.filin.app"
        minSdk = 26
        targetSdk = 36
        versionCode = 14
        versionName = "0.6.0"
        // модель распознавания штрихкодов — родной код под каждый процессор; телефонам нужны только ARM,
        // x86 — для эмуляторов, и он вдвое раздувает APK
        ndk { abiFilters += listOf("arm64-v8a", "armeabi-v7a") }
    }

    signingConfigs {
        if (signing != null) {
            create("release") {
                storeFile = file(signing.getProperty("storeFile"))
                storePassword = signing.getProperty("storePassword")
                keyAlias = signing.getProperty("keyAlias")
                keyPassword = signing.getProperty("keyPassword")
            }
        }
    }

    buildTypes {
        release {
            // Без ключа выпуск соберётся неподписанным — такой не выложит publish.ps1 (проверка apksigner),
            // а отладочная сборка ключа не требует вовсе. Падать здесь нельзя: этот блок
            // выполняется при любой сборке, и без ключа не собралась бы даже отладочная.
            signingConfig = signingConfigs.findByName("release")
        }
    }

    buildFeatures {
        buildConfig = true
    }
}

dependencies {
    testImplementation("junit:junit:4.13.2") // юнит-тесты без телефона: разбор QR привязки
    implementation("androidx.activity:activity-ktx:1.13.0")
    implementation("androidx.core:core-ktx:1.19.1")
    implementation("androidx.camera:camera-camera2:1.6.2")
    implementation("androidx.camera:camera-lifecycle:1.6.2")
    implementation("androidx.camera:camera-view:1.6.2")
    // встроенная модель: распознаёт без сети и без сервисов Google Play
    implementation("com.google.mlkit:barcode-scanning:17.3.0")
}
