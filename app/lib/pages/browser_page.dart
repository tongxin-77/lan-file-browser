import 'package:material_ui/material_ui.dart';

import '../api/api_exception.dart';
import '../api/lfb_client.dart';
import '../models/file_item.dart';
import '../models/share.dart';
import '../theme.dart';
import '../widgets/file_tile.dart';
import '../services/playback.dart';
import '../widgets/lamp_glow.dart';
import '../widgets/thumb.dart';
import 'image_viewer_page.dart';
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
  bool _grid = false;

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
        Navigator.of(context)
            .push(
              MaterialPageRoute<void>(
                builder: (_) => PlayerPage(
                  client: widget.client,
                  shareId: widget.share.id,
                  path: path,
                  title: item.name,
                  startAt: resumePosition(item.progress),
                ),
              ),
            )
            .then((_) {
              // ETag 不含进度：不带 If-None-Match 重新取，刷新进度条
              if (!mounted) return;
              _etag = null;
              _refresh();
            });
      case FileKind.image:
        final images = _items.where((i) => i.kind == FileKind.image).toList();
        Navigator.of(context).push(
          MaterialPageRoute<void>(
            builder: (_) => ImageViewerPage(
              client: widget.client,
              share: widget.share,
              dir: _path,
              images: images,
              initialIndex: images.indexOf(item),
            ),
          ),
        );
      default:
        _toast('暂不支持打开');
    }
  }

  /// 长按菜单：目前只有“删除”，仅在共享有删除权限（perms.modify）时提供。
  VoidCallback? _longPress(FileItem item) =>
      widget.share.perms.modify ? () => _itemMenu(item) : null;

  Future<void> _itemMenu(FileItem item) async {
    final delete = await showModalBottomSheet<bool>(
      context: context,
      backgroundColor: NightColors.pillow,
      showDragHandle: true,
      builder: (ctx) => SafeArea(
        child: ListTile(
          leading: const Icon(Icons.delete_outline),
          title: const Text('删除'),
          onTap: () => Navigator.of(ctx).pop(true),
        ),
      ),
    );
    if (delete == true && mounted) _confirmDelete(item);
  }

  Future<void> _confirmDelete(FileItem item) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text('删除“${item.name}”？'),
        content: Text(
          item.isDir ? '文件夹及其中的全部内容将移入电脑的回收站。' : '将移入电脑的回收站，可在电脑上还原。',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('取消'),
          ),
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('删除'),
          ),
        ],
      ),
    );
    if (ok != true || !mounted) return;
    final path = joinPath(_path, item.name);
    try {
      final failed = await widget.client.deleteFiles(widget.share.id, [path]);
      if (!mounted) return;
      if (failed.isEmpty) {
        if (item.hasThumb) {
          evictServerThumbs(
            widget.client,
            widget.share.id,
            path,
            item.mtime,
            ver: item.thumbVer,
          );
        }
        _toast('已移入电脑的回收站');
      } else {
        _toast(deleteErrorText(failed.first.code, failed.first.message));
      }
    } on ApiException catch (e) {
      if (mounted) _toast(deleteErrorText(e.code, e.message));
    } on LfbException catch (e) {
      if (mounted) _toast(e.message);
    }
    if (!mounted) return;
    _etag = null;
    _refresh();
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
          : _grid
          ? GridView.builder(
              controller: _scroll,
              physics: const AlwaysScrollableScrollPhysics(),
              padding: const EdgeInsets.symmetric(horizontal: 12),
              gridDelegate: const SliverGridDelegateWithMaxCrossAxisExtent(
                maxCrossAxisExtent: 180,
                childAspectRatio: 0.9,
              ),
              itemCount: _items.length,
              itemBuilder: (context, i) {
                final item = _items[i];
                return FileGridTile(
                  item: item,
                  thumbnail: _thumb(item),
                  onTap: () => _open(item),
                  onLongPress: _longPress(item),
                );
              },
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
                return FileTile(
                  item: item,
                  thumbnail: _thumb(item, width: 64),
                  onTap: () => _open(item),
                  onLongPress: _longPress(item),
                );
              },
            ),
    );
  }

  /// hasThumb 时的缩略图（03 §4.5）；否则 null，用类别图标。
  Widget? _thumb(FileItem item, {double? width}) {
    if (!item.hasThumb) return null;
    return ServerThumb(
      client: widget.client,
      share: widget.share.id,
      path: joinPath(_path, item.name),
      mtime: item.mtime,
      ver: item.thumbVer,
      width: width,
      placeholder: ThumbPlaceholder(icon: kindIcon(item.kind)),
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
        IconButton(
          tooltip: _grid ? '列表' : '网格',
          icon: Icon(_grid ? Icons.view_list : Icons.grid_view),
          onPressed: () => setState(() => _grid = !_grid),
        ),
      ],
    );
  }
}

/// 删除失败的中文提示（03 §4.10）。
String deleteErrorText(String code, String fallback) => switch (code) {
  'IN_USE' => '文件正在播放或被电脑上的程序占用，关闭后再试',
  'FORBIDDEN' => '没有删除这个文件夹内容的权限',
  'NOT_FOUND' => '文件已不存在',
  'NO_RECYCLE_BIN' => '电脑上这个盘没有回收站或回收站放不下，为安全起见没有删除',
  _ => '删除失败：$fallback',
};
