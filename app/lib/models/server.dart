/// `GET /api/ping` 响应 `{id, name, version, needPair}`。
class PingInfo {
  const PingInfo({
    required this.id,
    required this.name,
    required this.version,
    required this.needPair,
  });

  factory PingInfo.fromJson(Map<String, dynamic> j) => PingInfo(
    id: j['id'] as String? ?? '',
    name: j['name'] as String? ?? '',
    version: j['version'] as String? ?? '',
    needPair: j['needPair'] as bool? ?? true,
  );

  final String id;
  final String name;
  final String version;
  final bool needPair;
}

/// `POST /api/pair` 响应 `{token, serverId, name}`。
class PairResult {
  const PairResult({
    required this.token,
    required this.serverId,
    required this.name,
  });

  factory PairResult.fromJson(Map<String, dynamic> j) => PairResult(
    token: j['token'] as String,
    serverId: j['serverId'] as String? ?? '',
    name: j['name'] as String? ?? '',
  );

  final String token;
  final String serverId;
  final String name;
}

/// 本地保存的已配对服务器（01 §6）；token 另存于 secure storage。
class SavedServer {
  const SavedServer({
    required this.serverId,
    required this.name,
    required this.lastIp,
    required this.port,
  });

  factory SavedServer.fromJson(Map<String, dynamic> j) => SavedServer(
    serverId: j['serverId'] as String,
    name: j['name'] as String? ?? '',
    lastIp: j['lastIp'] as String,
    port: j['port'] as int,
  );

  final String serverId;
  final String name;
  final String lastIp;
  final int port;

  Map<String, dynamic> toJson() => {
    'serverId': serverId,
    'name': name,
    'lastIp': lastIp,
    'port': port,
  };
}
