/// `GET /api/ping` 响应 `{id, name, version, minAppVersion, needPair}`。
class PingInfo {
  const PingInfo({
    required this.id,
    required this.name,
    required this.version,
    required this.needPair,
    this.minAppVersion,
  });

  factory PingInfo.fromJson(Map<String, dynamic> j) => PingInfo(
    id: j['id'] as String? ?? '',
    name: j['name'] as String? ?? '',
    version: j['version'] as String? ?? '',
    needPair: j['needPair'] as bool? ?? true,
    minAppVersion: j['minAppVersion'] as String?,
  );

  final String id;
  final String name;
  final String version;
  final bool needPair;

  /// 服务端要求的最低 App 版本（02 §5.3）；旧服务端无此字段。
  final String? minAppVersion;
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
    this.ips = const [],
  });

  factory SavedServer.fromJson(Map<String, dynamic> j) => SavedServer(
    serverId: j['serverId'] as String,
    name: j['name'] as String? ?? '',
    lastIp: j['lastIp'] as String,
    port: j['port'] as int,
    ips: (j['ips'] as List?)?.cast<String>() ?? const [],
  );

  final String serverId;
  final String name;
  final String lastIp;
  final int port;

  /// 其他已知地址（二维码里的全部候选 IP 等），重连时与 [lastIp] 一起尝试。
  final List<String> ips;

  /// 重连候选：上次成功地址在前，去重。
  List<String> get candidates => {lastIp, ...ips}.toList();

  /// 换到 [ip]（可同时改端口）后的记录；旧地址保留为候选。
  SavedServer withAddress(String ip, [int? port]) => SavedServer(
    serverId: serverId,
    name: name,
    lastIp: ip,
    port: port ?? this.port,
    ips: candidates,
  );

  Map<String, dynamic> toJson() => {
    'serverId': serverId,
    'name': name,
    'lastIp': lastIp,
    'port': port,
    'ips': ips,
  };
}
