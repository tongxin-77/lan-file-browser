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
}
