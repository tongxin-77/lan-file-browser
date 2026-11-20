import 'package:material_ui/material_ui.dart';

import '../api/api_exception.dart';
import '../api/lfb_client.dart';
import '../models/file_item.dart';
import '../models/share.dart';
import '../theme.dart';
import '../widgets/file_tile.dart';
import '../widgets/lamp_glow.dart';
import 'player_page.dart';

/// 文件夹浏览（03 §4.2）：分页加载、下拉刷新（If-None-Match）、底部面包屑。
class BrowserPage extends StatefulWidget {
  const BrowserPage({
    super.key,
    required this.client,
    required this.share,
    this.pageSize = 200,
  });

  final LfbClient client;
  final Share share;
  final int pageSize;

  @override
  State<BrowserPage> createState() => _BrowserPageState();
}

class _BrowserPageState extends State<BrowserPage> {
  final _scroll = ScrollController();
  String _path = '';
  List<FileItem> _items = [];
  int _total = 0;
  String? _etag; // 第一页的 ETag：服务端按完整列表计算，304 表示整个目录未变
  bool _loading = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _scroll.addListener(() {
      if (_scroll.position.extentAfter < 600) _loadMore();
    });
    _go('');
  }

  @override
  void dispose() {
    _scroll.dispose();
    super.dispose();
  }

  Future<void> _go(String path) async {
    setState(() {
      _path = path;
      _items = [];
      _total = 0;
      _etag = null;
      _error = null;
    });
    if (_scroll.hasClients) _scroll.jumpTo(0);
    await _refresh();
  }

  Future<void> _refresh() async {
    final path = _path;
    setState(() => _loading = true);
    try {
      final page = await widget.client.list(
        widget.share.id,
        path,
        limit: widget.pageSize,
        etag: _etag,
      );
      if (!mounted || path != _path) return;
      setState(() {
        _error = null;
        if (page != null) {
          _items = page.items;
          _total = page.total;
          _etag = page.etag;
        }
      });
    } on LfbException catch (e) {
      if (mounted && path == _path) setState(() => _error = e.message);
    } finally {
      if (mounted && path == _path) setState(() => _loading = false);
    }
  }

  Future<void> _loadMore() async {
    if (_loading || _error != null || _items.length >= _total) return;
    final path = _path;
    setState(() => _loading = true);
    try {
      final page = await widget.client.list(
        widget.share.id,
        path,
        offset: _items.length,
        limit: widget.pageSize,
      );
      if (!mounted || path != _path || page == null) return;
      setState(() {
        _items = [..._items, ...page.items];
        _total = page.total;
      });
    } on LfbException catch (e) {
      if (mounted) _toast(e.message);
    } finally {
      if (mounted && path == _path) setState(() => _loading = false);
    }
  }

  void _toast(String msg) =>
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));

  void _open(FileItem item) {
    final path = joinPath(_path, item.name);
    switch (item.kind) {
      case FileKind.dir:
        _go(path);
      case FileKind.video:
        Navigator.of(context).push(
          MaterialPageRoute<void>(
            builder: (_) => PlayerPage(
              client: widget.client,
              shareId: widget.share.id,
              path: path,
              title: item.name,
            ),
          ),
        );
      default:
        _toast('暂不支持打开');
    }
  }

  @override
  Widget build(BuildContext context) {
    final segments = _path.isEmpty ? <String>[] : _path.split('/');
    final title = segments.isEmpty ? widget.share.name : segments.last;
    return PopScope(
      canPop: _path.isEmpty,
      onPopInvokedWithResult: (didPop, _) {
        if (!didPop) _go(parentPath(_path));
      },
      child: Scaffold(
        body: Column(
          children: [
            SafeArea(
              bottom: false,
              child: Padding(
                padding: const EdgeInsets.fromLTRB(24, 24, 24, 8),
                child: Align(
                  alignment: Alignment.centerLeft,
                  child: Text(
                    title,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: Theme.of(context).textTheme.headlineMedium,
                  ),
                ),
              ),
            ),
            Expanded(child: _body()),
            LampGlow(child: _breadcrumbs(segments)),
          ],
        ),
      ),
    );
  }

  Widget _body() {
    if (_error != null && _items.isEmpty) {
      return Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Padding(
              padding: const EdgeInsets.all(24),
              child: Text(_error!, textAlign: TextAlign.center),
            ),
            TextButton(onPressed: _refresh, child: const Text('重试')),
          ],
        ),
      );
    }
    if (_items.isEmpty && _loading) {
      return const Center(child: CircularProgressIndicator());
    }
    return RefreshIndicator(
      onRefresh: _refresh,
      child: _items.isEmpty
          ? ListView(
              physics: const AlwaysScrollableScrollPhysics(),
              children: const [
                Padding(
                  padding: EdgeInsets.all(32),
                  child: Text('空文件夹', style: TextStyle(color: NightColors.ash)),
                ),
              ],
            )
          : ListView.builder(
              controller: _scroll,
              physics: const AlwaysScrollableScrollPhysics(),
              itemCount: _items.length + (_items.length < _total ? 1 : 0),
              itemBuilder: (context, i) {
                if (i == _items.length) {
                  return const Padding(
                    padding: EdgeInsets.all(16),
                    child: Center(child: CircularProgressIndicator()),
                  );
                }
                final item = _items[i];
                return FileTile(item: item, onTap: () => _open(item));
              },
            ),
    );
  }

  /// 底部拇指区：返回上级 + 面包屑（共享名 / 各级目录）。
  Widget _breadcrumbs(List<String> segments) {
    final crumbs = <(String, String)>[('', widget.share.name)];
    for (var i = 0; i < segments.length; i++) {
      crumbs.add((segments.sublist(0, i + 1).join('/'), segments[i]));
    }
    return Row(
      children: [
        IconButton(
          tooltip: '返回上级',
          icon: const Icon(Icons.arrow_upward),
          onPressed: () => _path.isEmpty
              ? Navigator.of(context).maybePop()
              : _go(parentPath(_path)),
        ),
        Expanded(
          child: SingleChildScrollView(
            scrollDirection: Axis.horizontal,
            reverse: true, // 默认停在最深一级
            child: Row(
              children: [
                for (final (i, (path, name)) in crumbs.indexed) ...[
                  if (i > 0)
                    const Icon(
                      Icons.chevron_right,
                      size: 18,
                      color: NightColors.ash,
                    ),
                  TextButton(
                    onPressed: path == _path ? null : () => _go(path),
                    style: TextButton.styleFrom(
                      foregroundColor: NightColors.moon,
                      disabledForegroundColor: NightColors.wick,
                      minimumSize: const Size(48, 48),
                    ),
                    child: Text(name),
                  ),
                ],
              ],
            ),
          ),
        ),
      ],
    );
  }
}
