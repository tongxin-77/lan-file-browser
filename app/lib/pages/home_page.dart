import 'package:flutter/gestures.dart';
import 'package:material_ui/material_ui.dart';

import '../api/api_exception.dart';
import '../api/lfb_client.dart';
import '../app.dart';
import '../models/continue_item.dart';
import '../models/server.dart';
import '../models/share.dart';
import '../services/playback.dart';
import '../theme.dart';
import '../widgets/continue_tile.dart';
import '../widgets/lamp_glow.dart';
import '../widgets/password_sheet.dart';
import '../widgets/thumb.dart';
import 'browser_page.dart';
import 'player_page.dart';

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
  List<ContinueItem> _continue = const [];
  Set<String> _vaultIds = const {};

  @override
  void initState() {
    super.initState();
    _load();
    vaultController.addListener(_onVault);
  }

  /// 继续观看（03 §4.8）；失败不影响首页，保留旧列表。
  Future<void> _loadContinue() async {
    final unlocked = vaultController.unlocked;
    try {
      final items = await widget.client.continueWatching();
      // 请求期间上锁/解锁：丢弃结果，避免把片库条目放回列表
      if (unlocked != vaultController.unlocked) return;
      if (mounted) setState(() => _continue = items);
    } on LfbException {
      // 忽略
    }
  }

  /// 看完且有下一集的直接播下一集，否则从上次位置继续。
  void _play(ContinueItem c) {
    final t = c.target;
    Navigator.of(context)
        .push(
          MaterialPageRoute<void>(
            builder: (_) => PlayerPage(
              client: widget.client,
              shareId: c.share,
              path: t.path,
              title: t.name,
              startAt: resumePosition(t.progress),
            ),
          ),
        )
        .then((_) => _loadContinue());
  }

  @override
  void dispose() {
    vaultController.removeListener(_onVault);
    super.dispose();
  }

  /// 解锁时记下片库 id 并刷新继续观看（合并片库条目）；上锁时立即移除这些条目（04 §8）。
  void _onVault() {
    if (!mounted) return;
    if (vaultController.unlocked) {
      _vaultIds = {for (final s in vaultController.shares) s.id};
    } else {
      final gone = _vaultIds;
      _vaultIds = const {};
      _continue = [
        for (final c in _continue)
          if (!gone.contains(c.share)) c,
      ];
    }
    setState(() {});
    _loadContinue();
  }

  /// 长按电脑名称约 0.6 秒：输入密码（04 §4；按下时不给任何反馈）。
  void _askPassword() => showPasswordSheet(
    context,
    (pw) => vaultController.unlock(widget.client, pw),
  );

  Future<void> _load() async {
    _loadContinue();
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
                      child: RawGestureDetector(
                        gestures: {
                          LongPressGestureRecognizer:
                              GestureRecognizerFactoryWithHandlers<
                                LongPressGestureRecognizer
                              >(
                                () => LongPressGestureRecognizer(
                                  duration: const Duration(milliseconds: 600),
                                ),
                                (r) => r.onLongPress = _askPassword,
                              ),
                        },
                        child: Text(
                          widget.server.name,
                          style: text.headlineMedium,
                        ),
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
                    if (_continue.isNotEmpty) ...[
                      const Padding(
                        padding: EdgeInsets.fromLTRB(16, 0, 16, 4),
                        child: Text(
                          '接着看',
                          style: TextStyle(color: NightColors.ash),
                        ),
                      ),
                      for (final c in _continue)
                        ContinueTile(
                          item: c,
                          onTap: () => _play(c),
                          art: ServerThumb(
                            client: widget.client,
                            share: c.share,
                            path: c.target.path,
                            mtime: c.playsNext
                                ? c.next!.item.mtime
                                : DateTime.fromMillisecondsSinceEpoch(0),
                            width: 112,
                            placeholder: const ThumbPlaceholder(
                              icon: Icons.movie_outlined,
                              color: NightColors.wick,
                            ),
                          ),
                        ),
                      const SizedBox(height: 16),
                    ],
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
                    ..._vaultTiles(),
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

  /// 已解锁片库（与普通文件夹同款，名字后一个很淡的空心小点）+“现在收起”（a4 设计 2b）。
  List<Widget> _vaultTiles() {
    if (!vaultController.unlocked) return const [];
    return [
      for (final s in vaultController.shares)
        ListTile(
          leading: Icon(
            Icons.folder_outlined,
            color: s.online ? NightColors.wick : NightColors.seam,
          ),
          title: Row(
            children: [
              Flexible(child: Text(s.name, overflow: TextOverflow.ellipsis)),
              Container(
                key: const Key('vault-mark'),
                width: 5,
                height: 5,
                margin: const EdgeInsets.only(left: 7),
                decoration: BoxDecoration(
                  shape: BoxShape.circle,
                  border: Border.all(
                    color: NightColors.ash.withValues(alpha: 0.8),
                  ),
                ),
              ),
            ],
          ),
          subtitle: s.online
              ? null
              : const Text('离线', style: TextStyle(color: NightColors.ash)),
          onTap: () => _open(s),
        ),
      ListTile(
        title: const Text('现在收起'),
        subtitle: const Text(
          '离开 App、锁屏或 10 分钟不动，也会自动收起',
          style: TextStyle(color: NightColors.ash, fontSize: 12, height: 1.4),
        ),
        onTap: vaultController.lock,
      ),
    ];
  }
}
