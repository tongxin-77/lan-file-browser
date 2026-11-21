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
    final token = await _token(id);
    return token == null ? null : (s, token);
  }

  /// 全部有 token 的已保存服务器（连接页“已保存的电脑”）。
  Future<List<(SavedServer, String)>> paired() async => [
    for (final s in await servers())
      if (await _token(s.serverId) case final t?) (s, t),
  ];

  /// 设为上次使用（切换电脑，不删除其他已保存的）。
  Future<void> setLast(String serverId) async =>
      (await SharedPreferences.getInstance()).setString(_kLast, serverId);

  /// 读取失败（如 Keystore 异常）按无 token 处理，不抛出。
  Future<String?> _token(String serverId) async {
    try {
      return await _secure.read(key: _tokenKey(serverId));
    } on Object {
      return null;
    }
  }

  /// 保存/更新服务器并设为上次使用；[token] 非空时一并写入。
  /// 同一 serverId 已有记录时合并已知地址。
  Future<void> save(SavedServer server, {String? token}) async {
    final prefs = await SharedPreferences.getInstance();
    final old = (await servers()).where((s) => s.serverId == server.serverId);
    final merged = SavedServer(
      serverId: server.serverId,
      name: server.name,
      lastIp: server.lastIp,
      port: server.port,
      ips: {
        ...server.ips,
        for (final o in old) ...o.candidates,
      }.where((ip) => ip != server.lastIp).toList(),
    );
    final list = (await servers())
      ..removeWhere((s) => s.serverId == server.serverId)
      ..add(merged);
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
