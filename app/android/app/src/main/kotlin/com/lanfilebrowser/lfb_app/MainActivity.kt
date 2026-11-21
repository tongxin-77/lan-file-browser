package com.lanfilebrowser.lfb_app

import android.content.Context
import android.content.Intent
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.net.Uri
import android.net.wifi.WifiManager
import android.os.Build
import android.provider.Settings
import android.view.WindowManager
import androidx.core.content.FileProvider
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel
import java.io.File
import java.net.Inet4Address

class MainActivity : FlutterActivity() {
    private var wifiLock: WifiManager.WifiLock? = null
    private var multicastLock: WifiManager.MulticastLock? = null
    private var wifiCallback: ConnectivityManager.NetworkCallback? = null

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        bindProcessToWifi()
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, "com.lanfilebrowser/platform")
            .setMethodCallHandler { call, result ->
                when (call.method) {
                    "acquireWifiLock" -> {
                        acquireWifiLock()
                        result.success(null)
                    }
                    "releaseWifiLock" -> {
                        releaseWifiLock()
                        result.success(null)
                    }
                    "isVpnActive" -> result.success(isVpnActive())
                    // 局域网自动发现（docs/01-connection.md 2.2）
                    "acquireMulticastLock" -> {
                        acquireMulticastLock()
                        result.success(null)
                    }
                    "releaseMulticastLock" -> {
                        releaseMulticastLock()
                        result.success(null)
                    }
                    "wifiAddress" -> result.success(wifiAddress())
                    "deviceName" -> result.success("${Build.MANUFACTURER} ${Build.MODEL}")
                    // 加密片库：最近任务缩略图空白、禁止截屏（docs/04-vault.md 8）
                    "setSecure" -> {
                        if (call.arguments == true) {
                            window.addFlags(WindowManager.LayoutParams.FLAG_SECURE)
                        } else {
                            window.clearFlags(WindowManager.LayoutParams.FLAG_SECURE)
                        }
                        result.success(null)
                    }
                    "canInstallPackages" -> result.success(
                        Build.VERSION.SDK_INT < Build.VERSION_CODES.O ||
                            packageManager.canRequestPackageInstalls()
                    )
                    "openInstallSettings" -> {
                        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                            startActivity(
                                Intent(
                                    Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
                                    Uri.parse("package:$packageName"),
                                )
                            )
                        }
                        result.success(null)
                    }
                    "installApk" -> {
                        try {
                            installApk(File(call.arguments as String))
                            result.success(null)
                        } catch (e: Exception) {
                            result.error("INSTALL_FAILED", e.message, null)
                        }
                    }
                    else -> result.notImplemented()
                }
            }
    }

    // 经 FileProvider 把缓存目录 apk/ 下的安装包交给系统安装器（docs/02-release.md 5.2）
    private fun installApk(file: File) {
        val uri = FileProvider.getUriForFile(this, "$packageName.apkprovider", file)
        val intent = Intent(Intent.ACTION_VIEW)
            .setDataAndType(uri, "application/vnd.android.package-archive")
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
        startActivity(intent)
    }

    // 播放期间保持 Wi-Fi 不省电（docs/05-playback.md 2.4）：API 29+ 用 LOW_LATENCY，更早回退 HIGH_PERF
    private fun acquireWifiLock() {
        if (wifiLock?.isHeld == true) return
        val wm = applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager
        val mode = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            WifiManager.WIFI_MODE_FULL_LOW_LATENCY
        } else {
            @Suppress("DEPRECATION")
            WifiManager.WIFI_MODE_FULL_HIGH_PERF
        }
        wifiLock = wm.createWifiLock(mode, "lfb:playback").apply {
            setReferenceCounted(false)
            acquire()
        }
    }

    private fun releaseWifiLock() {
        wifiLock?.let { if (it.isHeld) it.release() }
        wifiLock = null
    }

    private fun acquireMulticastLock() {
        if (multicastLock?.isHeld == true) return
        try {
            val wm = applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager
            multicastLock = wm.createMulticastLock("lfb:discovery").apply {
                setReferenceCounted(false)
                acquire()
            }
        } catch (ignored: Exception) {
        }
    }

    private fun releaseMulticastLock() {
        multicastLock?.let { if (it.isHeld) it.release() }
        multicastLock = null
    }

    // Wi-Fi / 有线（非 VPN）网络的 IPv4 "地址/前缀"，供 Dart 计算子网广播地址
    private fun wifiAddress(): String? {
        val cm = getSystemService(ConnectivityManager::class.java) ?: return null
        @Suppress("DEPRECATION")
        for (n in cm.allNetworks) {
            val caps = cm.getNetworkCapabilities(n) ?: continue
            if (caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) continue
            if (!caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) &&
                !caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET)
            ) continue
            val lp = cm.getLinkProperties(n) ?: continue
            for (la in lp.linkAddresses) {
                val a = la.address
                if (a is Inet4Address) return "${a.hostAddress}/${la.prefixLength}"
            }
        }
        return null
    }

    // 开着 VPN 时让整个进程绕过 VPN 走 Wi-Fi（docs/01-connection.md 7.1）。
    // 默认 NetworkRequest 带 NOT_VPN，只匹配底层 Wi-Fi；VPN 不允许绕过时绑定失败，忽略即可。
    private fun bindProcessToWifi() {
        val cm = getSystemService(ConnectivityManager::class.java) ?: return
        val request = NetworkRequest.Builder()
            .addTransportType(NetworkCapabilities.TRANSPORT_WIFI)
            .build()
        val callback = object : ConnectivityManager.NetworkCallback() {
            private var bound: Network? = null

            override fun onAvailable(network: Network) {
                try {
                    if (cm.bindProcessToNetwork(network)) bound = network
                } catch (ignored: Exception) {
                }
            }

            override fun onLost(network: Network) {
                if (network != bound) return
                bound = null
                try {
                    cm.bindProcessToNetwork(null)
                } catch (ignored: Exception) {
                }
            }
        }
        try {
            cm.registerNetworkCallback(request, callback)
            wifiCallback = callback
        } catch (ignored: Exception) {
        }
    }

    private fun isVpnActive(): Boolean {
        val cm = getSystemService(ConnectivityManager::class.java) ?: return false
        @Suppress("DEPRECATION")
        return cm.allNetworks.any {
            cm.getNetworkCapabilities(it)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == true
        }
    }

    override fun onDestroy() {
        releaseWifiLock()
        releaseMulticastLock()
        wifiCallback?.let {
            try {
                getSystemService(ConnectivityManager::class.java)?.unregisterNetworkCallback(it)
            } catch (ignored: Exception) {
            }
        }
        wifiCallback = null
        super.onDestroy()
    }
}
