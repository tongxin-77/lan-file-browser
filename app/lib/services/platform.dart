import 'package:flutter/services.dart';

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
