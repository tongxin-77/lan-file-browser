/// 二维码内容 `lfb://connect?id=&name=&port=&ips=&code=`（docs/01-connection.md 2.1）。
class ConnectUri {
  const ConnectUri({
    required this.serverId,
    required this.name,
    required this.port,
    required this.ips,
    required this.code,
  });

  final String serverId;
  final String name;
  final int port;
  final List<String> ips;
  final String code;

  /// 解析二维码文本；格式不符返回 null。
  static ConnectUri? tryParse(String text) {
    final uri = Uri.tryParse(text.trim());
    if (uri == null || uri.scheme != 'lfb' || uri.host != 'connect') {
      return null;
    }
    final q = uri.queryParameters;
    final id = q['id'] ?? '';
    final code = q['code'] ?? '';
    final port = int.tryParse(q['port'] ?? '');
    final ips = (q['ips'] ?? '')
        .split(',')
        .map((s) => s.trim())
        .where((s) => s.isNotEmpty)
        .toList();
    if (id.isEmpty || code.isEmpty || ips.isEmpty) return null;
    if (port == null || port < 1 || port > 65535) return null;
    return ConnectUri(
      serverId: id,
      name: q['name'] ?? '',
      port: port,
      ips: ips,
      code: code,
    );
  }
}

/// 解析手动输入的 `IP[:端口]`，端口缺省 8686（01 §2.3）；非法返回 null。
(String, int)? parseHostPort(String input, {int defaultPort = 8686}) {
  final s = input.trim();
  if (s.isEmpty || s.contains('/') || s.contains(' ')) return null;
  final i = s.lastIndexOf(':');
  final host = i < 0 ? s : s.substring(0, i);
  final port = i < 0 ? defaultPort : int.tryParse(s.substring(i + 1));
  if (host.isEmpty || port == null || port < 1 || port > 65535) return null;
  return (host, port);
}
