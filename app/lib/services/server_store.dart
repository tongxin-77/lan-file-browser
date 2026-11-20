import 'dart:convert';
import 'dart:math';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../models/server.dart';

/// 本地持久化：已配对服务器（shared_preferences）、token（flutter_secure_storage，
/// Android Keystore）、本机 deviceId。
class ServerStore {
  ServerStore({FlutterSecureStorage? secure})
    : _secure = secure ?? const FlutterSecureStorage();

  final FlutterSecureStorage _secure;

  static const _kServers = 'servers';
  static const _kLast = 'lastServerId';
  static const _kDeviceId = 'deviceId';

  Future<List<SavedServer>> servers() async {
    final prefs = await SharedPreferences.getInstance();
    final raw = prefs.getString(_kServers);
    if (raw == null) return [];
    return (jsonDecode(raw) as List)
        .map((e) => SavedServer.fromJson((e as Map).cast<String, dynamic>()))
        .toList();
  }

  /// 上次使用的服务器（及其 token）；没有或 token 缺失返回 null。
  Future<(SavedServer, String)?> lastServer() async {
    final prefs = await SharedPreferences.getInstance();
    final id = prefs.getString(_kLast);
    if (id == null) return null;
    final s = (await servers()).where((s) => s.serverId == id).firstOrNull;
    if (s == null) return null;
    final token = await _secure.read(key: _tokenKey(id));
    return token == null ? null : (s, token);
  }

  /// 保存/更新服务器并设为上次使用；[token] 非空时一并写入。
  Future<void> save(SavedServer server, {String? token}) async {
    final prefs = await SharedPreferences.getInstance();
    final list = (await servers())
      ..removeWhere((s) => s.serverId == server.serverId)
      ..add(server);
    await prefs.setString(
      _kServers,
      jsonEncode(list.map((s) => s.toJson()).toList()),
    );
    await prefs.setString(_kLast, server.serverId);
    if (token != null) {
      await _secure.write(key: _tokenKey(server.serverId), value: token);
    }
  }

  Future<void> clearToken(String serverId) =>
      _secure.delete(key: _tokenKey(serverId));

  /// App 首次运行生成的随机 UUID v4，配对时上报。
  Future<String> deviceId() async {
    final prefs = await SharedPreferences.getInstance();
    var id = prefs.getString(_kDeviceId);
    if (id == null) {
      id = _uuidV4();
      await prefs.setString(_kDeviceId, id);
    }
    return id;
  }

  static String _tokenKey(String serverId) => 'token_$serverId';

  static String _uuidV4() {
    final r = Random.secure();
    final b = List<int>.generate(16, (_) => r.nextInt(256));
    b[6] = (b[6] & 0x0f) | 0x40;
    b[8] = (b[8] & 0x3f) | 0x80;
    final h = b.map((x) => x.toRadixString(16).padLeft(2, '0')).join();
    return '${h.substring(0, 8)}-${h.substring(8, 12)}-${h.substring(12, 16)}-'
        '${h.substring(16, 20)}-${h.substring(20)}';
  }
}
