import 'dart:async';

import 'package:material_ui/material_ui.dart';

import '../api/api_exception.dart';
import '../app.dart';
import '../models/connect_uri.dart';
import '../models/server.dart';
import '../services/platform.dart';
import '../theme.dart';

/// 启动：有已保存服务器则尝试其全部已知地址，成功进首页（01 §6）。
/// 只有电脑明确表示 token 无效才清凭据回连接页（由 onUnauthorized 处理）；
/// 其他失败保留凭据，停在“正在连接”并按退避自动重试。
class StartupPage extends StatefulWidget {
  const StartupPage({super.key});

  @override
  State<StartupPage> createState() => _StartupPageState();
}

/// 电脑换了 IP：用旧 [token] 连 [host]:[port]，ping 的 serverId 一致且 token 有效
/// 才保存新地址并返回（旧地址保留为候选）；id 不符抛 [NetworkException]，
/// token 无效由 onUnauthorized 处理并抛 401。
Future<SavedServer> moveServer(
  SavedServer server,
  String token,
  String host,
  int port,
) async {
  final updated = server.withAddress(host, port);
  await clientFor(updated, token).verify(timeout: const Duration(seconds: 3));
  await serverStore.save(updated);
  return updated;
}

/// 自动重试间隔（秒），之后一直按最后一项。
const _retryDelays = [2, 4, 8, 16, 30];

class _StartupPageState extends State<StartupPage> {
  SavedServer? _server;
  String? _token;
  String? _error;
  bool _busy = true;
  int _attempt = 0;
  Timer? _timer;

  @override
  void initState() {
    super.initState();
    _start();
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  Future<void> _start() async {
    final last = await serverStore.lastServer();
    if (last == null) {
      showConnectPage();
      return;
    }
    if (!mounted) return;
    setState(() {
      _server = last.$1;
      _token = last.$2;
    });
    await _connect();
  }

  Future<void> _connect() async {
    final server = _server!;
    _timer?.cancel();
    setState(() => _busy = true);
    final client = clientFor(server, _token!);
    try {
      await client.reconnect();
      if (!mounted) return;
      openHome(server.withAddress(client.host, client.port), _token!);
    } on ApiException catch (e) {
      // 401 已由 onUnauthorized 清凭据并跳转连接页
      if (!e.isUnauthorized) _fail(e.message);
    } on LfbException {
      _fail(
        '连不上「${server.name}」（${server.candidates.join('、')}）。'
        '请确认电脑已开机且在同一网络；电脑换了 IP 可点“修改地址”。',
      );
    }
  }

  Future<void> _fail(String message) async {
    final text = await connectionErrorText(message);
    if (!mounted) return;
    final delay = _retryDelays[_attempt.clamp(0, _retryDelays.length - 1)];
    _attempt++;
    setState(() {
      _busy = false;
      _error = text;
    });
    _timer = Timer(Duration(seconds: delay), _connect);
  }

  /// 电脑换了 IP：用旧 token 连新地址，serverId 一致才保存（免重新配对）。
  Future<void> _editAddress() async {
    final server = _server!;
    final input = await showDialog<String>(
      context: context,
      builder: (_) =>
          _AddressDialog(initial: '${server.lastIp}:${server.port}'),
    );
    final hp = input == null ? null : parseHostPort(input);
    if (hp == null || !mounted) return;
    final (host, port) = hp;
    _timer?.cancel();
    setState(() => _busy = true);
    try {
      final updated = await moveServer(server, _token!, host, port);
      if (mounted) openHome(updated, _token!);
    } on ApiException catch (e) {
      if (!e.isUnauthorized) _fail(e.message);
    } on LfbException catch (e) {
      _fail('$host:$port：${e.message}');
    }
  }

  @override
  Widget build(BuildContext context) {
    final server = _server;
    if (server == null) {
      return const Scaffold(body: Center(child: CircularProgressIndicator()));
    }
    return Scaffold(
      body: SafeArea(
        child: ListView(
          padding: const EdgeInsets.fromLTRB(24, 48, 24, 24),
          children: [
            Text(
              '正在连接「${server.name}」…',
              style: Theme.of(context).textTheme.headlineSmall,
            ),
            const SizedBox(height: 16),
            if (_busy)
              const Align(
                alignment: Alignment.centerLeft,
                child: CircularProgressIndicator(),
              )
            else if (_error != null)
              Text(
                '$_error\n\n稍后自动重试。',
                style: const TextStyle(color: NightColors.ash, height: 1.5),
              ),
            const SizedBox(height: 24),
            Wrap(
              spacing: 12,
              runSpacing: 8,
              children: [
                FilledButton(
                  onPressed: _busy ? null : _connect,
                  child: const Text('重试'),
                ),
                OutlinedButton(
                  onPressed: _busy ? null : _editAddress,
                  child: const Text('修改地址'),
                ),
                TextButton(
                  onPressed: () => showConnectPage(),
                  child: const Text('切换或添加电脑'),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

class _AddressDialog extends StatefulWidget {
  const _AddressDialog({required this.initial});

  final String initial;

  @override
  State<_AddressDialog> createState() => _AddressDialogState();
}

class _AddressDialogState extends State<_AddressDialog> {
  late final _c = TextEditingController(text: widget.initial);

  @override
  void dispose() {
    _c.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: const Text('修改电脑地址'),
    content: TextField(
      key: const Key('new-addr'),
      controller: _c,
      autofocus: true,
      keyboardType: TextInputType.url,
      decoration: const InputDecoration(hintText: '如 192.168.1.5:8686'),
    ),
    actions: [
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('取消'),
      ),
      FilledButton(
        onPressed: () => Navigator.pop(context, _c.text),
        child: const Text('连接'),
      ),
    ],
  );
}
