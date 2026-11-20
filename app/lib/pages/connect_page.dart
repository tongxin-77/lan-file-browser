import 'package:flutter/services.dart';
import 'package:material_ui/material_ui.dart';

import '../api/api_exception.dart';
import '../api/lfb_client.dart';
import '../api/probe.dart';
import '../app.dart';
import '../models/connect_uri.dart';
import '../models/server.dart';
import '../services/platform.dart';
import '../theme.dart';
import '../widgets/lamp_glow.dart';
import 'scan_page.dart';
import 'startup_page.dart';

/// 连接页：扫码（首选）或手动输入 IP:端口 + 6 位 PIN（01 §2.1、2.3）。
class ConnectPage extends StatefulWidget {
  const ConnectPage({super.key, this.message, this.canRetry = false});

  /// 进入本页的原因（断线、授权失效等）。
  final String? message;

  /// 显示「重试上次连接」。
  final bool canRetry;

  @override
  State<ConnectPage> createState() => _ConnectPageState();
}

class _ConnectPageState extends State<ConnectPage> {
  final _addr = TextEditingController();
  final _pin = TextEditingController();
  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _addr.dispose();
    _pin.dispose();
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
      await _pair(ip, c.port, c.code);
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
      await _pair(host, port, _pin.text);
    });
  }

  Future<void> _pair(String host, int port, String code) async {
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
      if (mounted) setState(() => _error = e.message);
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
                    if (widget.canRetry)
                      Align(
                        alignment: Alignment.centerLeft,
                        child: TextButton(
                          onPressed: _busy
                              ? null
                              : () => Navigator.of(context).pushReplacement(
                                  MaterialPageRoute<void>(
                                    builder: (_) => const StartupPage(),
                                  ),
                                ),
                          child: const Text('重试上次连接'),
                        ),
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
