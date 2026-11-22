import 'package:flutter/services.dart';
import 'package:material_ui/material_ui.dart';

import '../api/api_exception.dart';
import '../api/lfb_client.dart';
import '../api/probe.dart';
import '../app.dart';
import '../models/connect_uri.dart';
import '../models/server.dart';
import '../services/discovery.dart';
import '../services/platform.dart';
import '../theme.dart';
import '../widgets/lamp_glow.dart';
import 'scan_page.dart';
import 'startup_page.dart';

/// 连接页：扫码（首选）或手动输入 IP:端口 + 6 位 PIN（01 §2.1、2.3）。
class ConnectPage extends StatefulWidget {
  const ConnectPage({super.key, this.message});

  /// 进入本页的原因（如电脑已将本机移除）。
  final String? message;

  @override
  State<ConnectPage> createState() => _ConnectPageState();
}

class _ConnectPageState extends State<ConnectPage> {
  final _addr = TextEditingController();
  final _pin = TextEditingController();
  bool _busy = false;
  String? _error;

  /// 已保存的电脑（及 token）：点即用已保存凭据连接，无需重新配对。
  List<(SavedServer, String)> _saved = const [];

  /// 局域网自动发现结果；null 表示正在扫描（01 §2.2）。
  List<DiscoveredServer>? _found;

  /// 点选的未配对电脑：填好地址，等用户输 PIN。
  DiscoveredServer? _picked;
  final _pinFocus = FocusNode();

  @override
  void initState() {
    super.initState();
    serverStore.paired().then((list) {
      if (mounted) setState(() => _saved = list);
    });
    _discover();
  }

  Future<void> _discover() async {
    setState(() => _found = null);
    final list = await lanDiscovery.scan();
    if (mounted) setState(() => _found = list);
  }

  /// 已配对的直接用已保存凭据连接发现到的地址（连上才保存）；未配对的填好地址等输 PIN。
  Future<void> _pick(DiscoveredServer d) async {
    final saved = _saved.where((p) => p.$1.serverId == d.id).firstOrNull;
    if (saved case (final s, final token)) {
      final found = SavedServer(
        serverId: s.serverId,
        name: s.name,
        lastIp: d.from,
        port: d.port,
        ips: {...d.ips, ...s.candidates}.toList(),
      );
      return _useSaved((found, token));
    }
    setState(() {
      _picked = d;
      _error = null;
      _addr.text = '${d.from}:${d.port}';
    });
    _pinFocus.requestFocus();
  }

  /// 只切到“正在连接”页；连接成功后才保存并设为当前电脑，失败不改任何已保存信息。
  void _useSaved((SavedServer, String) target) {
    Navigator.of(context).pushReplacement(
      MaterialPageRoute<void>(builder: (_) => StartupPage(target: target)),
    );
  }

  @override
  void dispose() {
    _addr.dispose();
    _pin.dispose();
    _pinFocus.dispose();
    super.dispose();
  }

  Future<void> _scan() async {
    final raw = await Navigator.of(context)
        .push<String>(MaterialPageRoute(builder: (_) => const ScanPage()));
    if (raw == null) return;
    final c = ConnectUri.tryParse(raw);
    if (c == null) {
      setState(() => _error = '这不是 LAN File Browser 的连接二维码');
      return;
    }
    await _run(() async {
      final ip = await probeFirst(c.ips, c.port, c.serverId);
      if (ip == null) {
        throw const NetworkException(
          '二维码里的地址都连不上。请确认手机与电脑在同一网络、防火墙已放行，或手动输入电脑 IP',
        );
      }
      await _pair(ip, c.port, c.code, ips: c.ips);
    });
  }

  Future<void> _manual() async {
    final hp = parseHostPort(_addr.text);
    if (hp == null) {
      setState(() => _error = '地址格式应为 192.168.1.5 或 192.168.1.5:8686');
      return;
    }
    if (!RegExp(r'^\d{6}$').hasMatch(_pin.text)) {
      setState(() => _error = '请输入电脑上显示的 6 位 PIN');
      return;
    }
    final (host, port) = hp;
    await _run(() async {
      await LfbClient(host: host, port: port).ping();
      final picked = _picked;
      await _pair(
        host,
        port,
        _pin.text,
        ips: picked != null && picked.from == host ? picked.ips : const [],
      );
    });
  }

  Future<void> _pair(
    String host,
    int port,
    String code, {
    List<String> ips = const [],
  }) async {
    final result = await LfbClient(host: host, port: port).pair(
      code: code,
      deviceName: await PlatformService.deviceName(),
      deviceId: await serverStore.deviceId(),
    );
    final server = SavedServer(
      serverId: result.serverId,
      name: result.name,
      lastIp: host,
      port: port,
      ips: ips,
    );
    await serverStore.save(server, token: result.token);
    openHome(server, result.token);
  }

  Future<void> _run(Future<void> Function() action) async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await action();
    } on ApiException catch (e) {
      if (mounted) {
        setState(
          () => _error = e.isUnauthorized ? '配对码错误或已过期，请刷新后重试' : e.message,
        );
      }
    } on LfbException catch (e) {
      final message = await connectionErrorText(e.message);
      if (mounted) setState(() => _error = message);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final text = Theme.of(context).textTheme;
    return Scaffold(
      body: Column(
        children: [
          // 上半屏安静区：只放信息
          Expanded(
            child: SafeArea(
              bottom: false,
              child: ListView(
                padding: const EdgeInsets.fromLTRB(24, 48, 24, 16),
                children: [
                  Text('连接电脑', style: text.headlineMedium),
                  const SizedBox(height: 12),
                  const Text(
                    '在电脑上打开 LAN File Browser，扫描显示的二维码；'
                    '或输入电脑 IP 与 6 位 PIN。',
                    style: TextStyle(color: NightColors.ash, height: 1.5),
                  ),
                  if (widget.message != null) ...[
                    const SizedBox(height: 20),
                    Text(widget.message!, style: const TextStyle(height: 1.5)),
                  ],
                  if (_saved.isNotEmpty) ...[
                    const SizedBox(height: 20),
                    const Text(
                      '已保存的电脑',
                      style: TextStyle(color: NightColors.ash),
                    ),
                    for (final (s, token) in _saved)
                      ListTile(
                        contentPadding: EdgeInsets.zero,
                        leading: const Icon(Icons.computer_outlined),
                        title: Text(s.name),
                        subtitle: Text(
                          '${s.lastIp}:${s.port}',
                          style: const TextStyle(color: NightColors.ash),
                        ),
                        enabled: !_busy,
                        onTap: () => _useSaved((s, token)),
                      ),
                  ],
                  const SizedBox(height: 20),
                  Row(
                    children: [
                      const Expanded(
                        child: Text(
                          '局域网中的电脑',
                          style: TextStyle(color: NightColors.ash),
                        ),
                      ),
                      if (_found == null)
                        const SizedBox.square(
                          dimension: 16,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      else
                        TextButton(
                          onPressed: _busy ? null : _discover,
                          child: const Text('重新扫描'),
                        ),
                    ],
                  ),
                  if (_found case final found? when found.isEmpty)
                    const Text(
                      '没有找到。路由器可能屏蔽了广播，可扫码或手动输入电脑 IP。',
                      style: TextStyle(color: NightColors.ash, height: 1.5),
                    ),
                  for (final d in _found ?? const <DiscoveredServer>[])
                    ListTile(
                      contentPadding: EdgeInsets.zero,
                      leading: const Icon(Icons.computer_outlined),
                      title: Text('${d.name} · ${d.from}'),
                      trailing: _saved.any((p) => p.$1.serverId == d.id)
                          ? const Text(
                              '已配对',
                              style: TextStyle(color: NightColors.moss),
                            )
                          : null,
                      enabled: !_busy,
                      onTap: () => _pick(d),
                    ),
                  if (_picked case final p?) ...[
                    const SizedBox(height: 8),
                    Text(
                      '请在下方输入「${p.name}」上显示的 6 位 PIN，然后点“配对”。',
                      style: const TextStyle(height: 1.5),
                    ),
                  ],
                  if (_error != null) ...[
                    const SizedBox(height: 20),
                    Text(
                      _error!,
                      style: TextStyle(
                        color: Theme.of(context).colorScheme.error,
                        height: 1.5,
                      ),
                    ),
                  ],
                ],
              ),
            ),
          ),
          // 下方灯晕拇指区：所有操作
          LampGlow(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                FilledButton.icon(
                  onPressed: _busy ? null : _scan,
                  icon: const Icon(Icons.qr_code_scanner),
                  label: const Text('扫码连接'),
                ),
                const SizedBox(height: 16),
                TextField(
                  key: const Key('addr'),
                  controller: _addr,
                  enabled: !_busy,
                  keyboardType: TextInputType.url,
                  decoration: const InputDecoration(
                    hintText: '电脑 IP，如 192.168.1.5:8686',
                  ),
                ),
                const SizedBox(height: 8),
                Row(
                  children: [
                    Expanded(
                      child: TextField(
                        key: const Key('pin'),
                        controller: _pin,
                        focusNode: _pinFocus,
                        enabled: !_busy,
                        keyboardType: TextInputType.number,
                        maxLength: 6,
                        inputFormatters: [
                          FilteringTextInputFormatter.digitsOnly,
                        ],
                        decoration: const InputDecoration(
                          hintText: '6 位 PIN',
                          counterText: '',
                        ),
                      ),
                    ),
                    const SizedBox(width: 12),
                    OutlinedButton(
                      onPressed: _busy ? null : _manual,
                      style: OutlinedButton.styleFrom(
                        minimumSize: const Size(96, 52),
                      ),
                      child: _busy
                          ? const SizedBox.square(
                              dimension: 20,
                              child: CircularProgressIndicator(strokeWidth: 2),
                            )
                          : const Text('配对'),
                    ),
                  ],
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
