import 'package:flutter/services.dart';

/// 连接失败且手机开着 VPN 时追加的提示（01 §7.1）。
const vpnHint =
    '手机开着 VPN，可能拦截了局域网连接。请在 VPN 里打开“绕过局域网/私有网络”，'
    '或在分应用代理中排除 LAN File Browser。';

/// [vpn] 为真时在连接错误 [message] 后另起一行追加 [vpnHint]。
String withVpnHint(String message, {required bool vpn}) =>
    vpn ? '$message\n$vpnHint' : message;

/// 查询 VPN 状态后为连接错误追加提示。
Future<String> connectionErrorText(String message) async =>
    withVpnHint(message, vpn: await PlatformService.isVpnActive());

/// Android 原生能力（MainActivity.kt 中的 MethodChannel）。
class PlatformService {
  static const _channel = MethodChannel('com.lanfilebrowser/platform');

  /// 播放期间持有 WifiLock（docs/05-playback.md 2.4）。失败不影响播放。
  static Future<void> acquireWifiLock() async {
    try {
      await _channel.invokeMethod<void>('acquireWifiLock');
    } on Exception {
      // 忽略：非 Android 或系统拒绝
    }
  }

  static Future<void> releaseWifiLock() async {
    try {
      await _channel.invokeMethod<void>('releaseWifiLock');
    } on Exception {
      // 同上
    }
  }

  /// 自动发现期间持有 MulticastLock，部分机型否则收不到广播回复（01 §2.2）。
  static Future<void> acquireMulticastLock() async {
    try {
      await _channel.invokeMethod<void>('acquireMulticastLock');
    } on Exception {
      // 忽略：非 Android 或系统拒绝
    }
  }

  static Future<void> releaseMulticastLock() async {
    try {
      await _channel.invokeMethod<void>('releaseMulticastLock');
    } on Exception {
      // 同上
    }
  }

  /// 当前 Wi-Fi / 有线网络的 IPv4 地址与前缀，如 `192.168.1.23/24`；无则 null。
  static Future<String?> wifiAddress() async {
    try {
      return await _channel.invokeMethod<String>('wifiAddress');
    } on Exception {
      return null;
    }
  }

  /// 手机当前是否开着 VPN（任一网络带 TRANSPORT_VPN）。
  static Future<bool> isVpnActive() async {
    try {
      return await _channel.invokeMethod<bool>('isVpnActive') ?? false;
    } on Exception {
      return false;
    }
  }

  /// 配对时上报的设备名，如 “Google Pixel 8”。
  static Future<String> deviceName() async {
    try {
      return await _channel.invokeMethod<String>('deviceName') ?? 'Android';
    } on Exception {
      return 'Android';
    }
  }

  /// 窗口 FLAG_SECURE：最近任务缩略图空白、禁止截屏（04 §8）。
  static Future<void> setSecure(bool on) async {
    try {
      await _channel.invokeMethod<void>('setSecure', on);
    } on Exception {
      // 忽略：非 Android
    }
  }

  /// 是否已允许本 App“安装未知应用”（Android 8+ 需用户授予）。
  static Future<bool> canInstallPackages() async {
    try {
      return await _channel.invokeMethod<bool>('canInstallPackages') ?? false;
    } on Exception {
      return false;
    }
  }

  /// 打开本 App 的“安装未知应用”设置页。
  static Future<void> openInstallSettings() async {
    try {
      await _channel.invokeMethod<void>('openInstallSettings');
    } on Exception {
      // 忽略
    }
  }

  /// 经 FileProvider 调用系统安装器安装 [path]（须位于应用缓存目录 apk/ 下）。
  static Future<bool> installApk(String path) async {
    try {
      await _channel.invokeMethod<void>('installApk', path);
      return true;
    } on Exception {
      return false;
    }
  }
}
