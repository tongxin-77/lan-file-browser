import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'platform.dart';

/// 自动发现回复 `{id,name,version,port,ips}`（01 §2.2），[from] 为回复的来源地址。
class DiscoveredServer {
  const DiscoveredServer({
    required this.id,
    required this.name,
    required this.version,
    required this.port,
    required this.ips,
    required this.from,
  });

  /// 解析一条回复；不是合法回复返回 null。
  static DiscoveredServer? tryParse(List<int> data, String from) {
    try {
      final j = (jsonDecode(utf8.decode(data)) as Map).cast<String, dynamic>();
      final id = j['id'];
      final port = j['port'];
      if (id is! String || id.isEmpty || port is! int) return null;
      return DiscoveredServer(
        id: id,
        name: j['name'] as String? ?? '',
        version: j['version'] as String? ?? '',
        port: port,
        ips: [
          for (final ip in (j['ips'] as List?) ?? const [])
            if (ip is String) ip,
        ],
        from: from,
      );
    } on Object {
      return null;
    }
  }

  final String id;
  final String name;
  final String version;
  final int port;
  final List<String> ips;
  final String from;

  /// 连接候选：来源地址优先（它能回复说明可达），再是回复里的全部 IP。
  List<String> get candidates => {from, ...ips}.toList();
}

/// 收到的原始回复：（来源地址，内容）。
typedef DiscoveryReply = (String from, List<int> data);

/// 向 [targets] 的 UDP 8687 发 `LFB_DISCOVER`，收集 [window] 内的回复。
typedef UdpProbe = Future<List<DiscoveryReply>> Function(
  List<String> targets,
  Duration window,
);

/// 局域网自动发现（01 §2.2）：广播到 255.255.255.255 与当前 Wi-Fi 子网广播地址。
class LanDiscovery {
  LanDiscovery({this.probe = udpProbe});

  /// 可替换的 UDP 收发（测试中 mock）。
  UdpProbe probe;

  /// 扫描一次，按 id 去重（同一台电脑多张网卡回复时合并地址）。
  Future<List<DiscoveredServer>> scan({
    Duration window = const Duration(seconds: 2),
  }) async {
    await PlatformService.acquireMulticastLock();
    try {
      final subnet = broadcastAddress(await PlatformService.wifiAddress());
      final replies = await probe(['255.255.255.255', ?subnet], window);
      return mergeReplies(replies);
    } finally {
      await PlatformService.releaseMulticastLock();
    }
  }

  /// 按 serverId 查找电脑的当前地址（IP 变化后免重新配对的主路径，01 §6）。
  Future<DiscoveredServer?> find(String serverId) async {
    final list = await scan();
    return list.where((s) => s.id == serverId).firstOrNull;
  }
}

/// 解析并按 id 去重，保持首次出现顺序。
List<DiscoveredServer> mergeReplies(List<DiscoveryReply> replies) {
  final byId = <String, DiscoveredServer>{};
  for (final (from, data) in replies) {
    final s = DiscoveredServer.tryParse(data, from);
    if (s == null) continue;
    final old = byId[s.id];
    byId[s.id] = old == null
        ? s
        : DiscoveredServer(
            id: old.id,
            name: old.name,
            version: old.version,
            port: old.port,
            ips: {...old.ips, s.from, ...s.ips}.toList(),
            from: old.from,
          );
  }
  return byId.values.toList();
}

/// `192.168.1.23/24` → `192.168.1.255`；格式不对或 /31、/32 返回 null。
String? broadcastAddress(String? cidr) {
  if (cidr == null) return null;
  final m = RegExp(r'^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})/(\d{1,2})$')
      .firstMatch(cidr);
  if (m == null) return null;
  final octets = [for (var i = 1; i <= 4; i++) int.parse(m.group(i)!)];
  final prefix = int.parse(m.group(5)!);
  if (octets.any((o) => o > 255) || prefix > 30) return null;
  final ip = octets.fold<int>(0, (a, o) => (a << 8) | o);
  final host = (1 << (32 - prefix)) - 1;
  final b = ip | host;
  return [for (var s = 24; s >= 0; s -= 8) (b >> s) & 0xff].join('.');
}

/// 真实 UDP：连发 3 次（间隔 500ms），收集 [window] 内的回复。
Future<List<DiscoveryReply>> udpProbe(
  List<String> targets,
  Duration window,
) async {
  final replies = <DiscoveryReply>[];
  final RawDatagramSocket socket;
  try {
    socket = await RawDatagramSocket.bind(InternetAddress.anyIPv4, 0);
  } on SocketException {
    return replies;
  }
  socket.broadcastEnabled = true;
  final sub = socket.listen((e) {
    if (e != RawSocketEvent.read) return;
    final d = socket.receive();
    if (d != null) replies.add((d.address.address, d.data));
  });
  final request = ascii.encode('LFB_DISCOVER');
  for (var i = 0; i < 3; i++) {
    for (final t in targets) {
      try {
        socket.send(request, InternetAddress(t), 8687);
      } on Object {
        // 某个广播地址发不出去（如无此网络）不影响其他
      }
    }
    if (i < 2) await Future<void>.delayed(const Duration(milliseconds: 500));
  }
  await Future<void>.delayed(window - const Duration(seconds: 1));
  await sub.cancel();
  socket.close();
  return replies;
}
