import 'dart:async';

import 'package:http/http.dart' as http;

import 'lfb_client.dart';

/// 对候选 IP 并发 `GET /api/ping`，返回第一个成功且 id 匹配的地址；
/// 全部失败返回 null（docs/01-connection.md 2.1）。
Future<String?> probeFirst(
  List<String> ips,
  int port,
  String serverId, {
  http.Client? httpClient,
  Duration timeout = const Duration(milliseconds: 1500),
}) {
  if (ips.isEmpty) return Future.value(null);
  final client = httpClient ?? http.Client();
  final done = Completer<String?>();
  if (httpClient == null) done.future.whenComplete(client.close);
  var pending = ips.length;
  for (final ip in ips) {
    LfbClient(host: ip, port: port, httpClient: client)
        .ping(timeout: timeout)
        .then((p) {
          if (p.id == serverId && !done.isCompleted) done.complete(ip);
        })
        .catchError((Object _) {})
        .whenComplete(() {
          if (--pending == 0 && !done.isCompleted) done.complete(null);
        });
  }
  return done.future;
}
