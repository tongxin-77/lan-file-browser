import 'package:material_ui/material_ui.dart';

import '../api/api_exception.dart';
import '../api/lfb_client.dart';
import '../app.dart';
import '../models/server.dart';
import '../models/share.dart';
import '../theme.dart';
import '../widgets/lamp_glow.dart';
import 'browser_page.dart';

/// 首页：电脑名、连接状态、共享列表（03 §4.1）。
class HomePage extends StatefulWidget {
  const HomePage({super.key, required this.server, required this.client});

  final SavedServer server;
  final LfbClient client;

  @override
  State<HomePage> createState() => _HomePageState();
}

class _HomePageState extends State<HomePage> {
  List<Share>? _shares;
  String? _error;
  bool _connected = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final shares = await widget.client.shares();
      if (!mounted) return;
      setState(() {
        _shares = shares;
        _error = null;
        _connected = true;
      });
    } on LfbException catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.message;
        _connected = e is! NetworkException;
      });
    }
  }

  void _open(Share s) {
    if (!s.online) {
      ScaffoldMessenger.of(context)
          .showSnackBar(const SnackBar(content: Text('该文件夹暂时不可用（硬盘未连接？）')));
      return;
    }
    Navigator.of(context).push(
      MaterialPageRoute<void>(
        builder: (_) => BrowserPage(client: widget.client, share: s),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final text = Theme.of(context).textTheme;
    final shares = _shares;
    return Scaffold(
      body: Column(
        children: [
          Expanded(
            child: SafeArea(
              bottom: false,
              child: RefreshIndicator(
                onRefresh: _load,
                child: ListView(
                  physics: const AlwaysScrollableScrollPhysics(),
                  padding: const EdgeInsets.fromLTRB(8, 40, 8, 16),
                  children: [
                    Padding(
                      padding: const EdgeInsets.symmetric(horizontal: 16),
                      child: Text(
                        widget.server.name,
                        style: text.headlineMedium,
                      ),
                    ),
                    const SizedBox(height: 8),
                    Padding(
                      padding: const EdgeInsets.symmetric(horizontal: 16),
                      child: Row(
                        children: [
                          Icon(
                            Icons.circle,
                            size: 8,
                            color: _connected
                                ? NightColors.moss
                                : NightColors.ash,
                          ),
                          const SizedBox(width: 8),
                          Text(
                            _connected
                                ? '已连接 · ${widget.server.lastIp}'
                                : '连接中断，下拉重试',
                            style: const TextStyle(color: NightColors.ash),
                          ),
                        ],
                      ),
                    ),
                    const SizedBox(height: 24),
                    if (_error != null)
                      Padding(
                        padding: const EdgeInsets.all(16),
                        child: Text(_error!),
                      ),
                    if (shares == null && _error == null)
                      const Padding(
                        padding: EdgeInsets.all(32),
                        child: Center(child: CircularProgressIndicator()),
                      ),
                    if (shares != null && shares.isEmpty)
                      const Padding(
                        padding: EdgeInsets.all(16),
                        child: Text(
                          '电脑上还没有对这台手机可见的共享文件夹。\n请在电脑的管理页添加。',
                          style: TextStyle(color: NightColors.ash, height: 1.5),
                        ),
                      ),
                    for (final s in shares ?? const <Share>[])
                      ListTile(
                        leading: Icon(
                          Icons.folder_outlined,
                          color: s.online ? NightColors.wick : NightColors.seam,
                        ),
                        title: Text(s.name),
                        subtitle: s.online
                            ? null
                            : const Text(
                                '离线',
                                style: TextStyle(color: NightColors.ash),
                              ),
                        onTap: () => _open(s),
                      ),
                  ],
                ),
              ),
            ),
          ),
          LampGlow(
            child: Align(
              alignment: Alignment.centerLeft,
              child: TextButton.icon(
                onPressed: () => showConnectPage(),
                icon: const Icon(Icons.swap_horiz),
                label: const Text('连接其他电脑'),
              ),
            ),
          ),
        ],
      ),
    );
  }
}
