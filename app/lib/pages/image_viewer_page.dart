import 'package:material_ui/material_ui.dart';
import 'package:photo_view/photo_view.dart';

import '../api/lfb_client.dart';
import '../models/file_item.dart';
import '../models/share.dart';
import '../services/fetch_queue.dart';
import '../services/image_saver.dart';
import '../services/orientation.dart';
import '../services/server_image.dart';
import '../services/thumbs.dart';
import '../theme.dart';
import '../widgets/file_tile.dart';
import '../widgets/progressive_image.dart';
import '../widgets/thumb.dart';

/// 看图（设计稿 5）：左右滑动、双指缩放、底部缩略图条与信息；共享允许下载时可保存到手机。
class ImageViewerPage extends StatefulWidget {
  const ImageViewerPage({
    super.key,
    required this.client,
    required this.share,
    required this.dir,
    required this.images,
    required this.initialIndex,
  });

  final LfbClient client;
  final Share share;
  final String dir;
  final List<FileItem> images;
  final int initialIndex;

  @override
  State<ImageViewerPage> createState() => _ImageViewerPageState();
}

class _ImageViewerPageState extends State<ImageViewerPage> {
  late final _pages = PageController(initialPage: widget.initialIndex);
  late final _strip = ScrollController(
    initialScrollOffset: widget.initialIndex * _stripCell,
  );
  late int _index = widget.initialIndex;
  bool _chrome = true;
  bool _saving = false;
  bool _landscape = false; // 其他页面竖屏；横图可点“旋转屏幕”横过来看

  static const _stripCell = 60.0;

  /// 已发出的相邻页预取（预览图）。
  List<ServerImage> _preloads = const [];

  /// 预览图已显示或失败的页（翻到这些页时立即开始预取相邻页）。
  final _settled = <int>{};

  @override
  void dispose() {
    _preload(null);
    _pages.dispose();
    _strip.dispose();
    if (_landscape) lockOrientation(landscape: false); // 离开时恢复竖屏
    super.dispose();
  }

  void _rotate() {
    setState(() => _landscape = !_landscape);
    lockOrientation(landscape: _landscape);
  }

  String _pathOf(FileItem f) => joinPath(widget.dir, f.name);

  FetchQueue get _queue => ImageStores.instance.viewerQueue;

  /// 预览档位：屏幕物理像素长边向上取 1280/1920/2560。
  int get _previewWidth => previewWidthFor(
    WidgetsBinding.instance.platformDispatcher.implicitView?.physicalSize ??
        Size.zero,
  );

  /// 预览图（服务端能生成缩略图的图片才有）。
  ServerImage? _preview(FileItem f, int w) => f.hasThumb
      ? serverImageProvider(
          widget.client,
          widget.share.id,
          widget.client.thumbUrl(
            widget.share.id,
            _pathOf(f),
            w,
            ver: f.thumbVer,
          ),
          f.mtime,
        )
      : null;

  ServerImage _originalImage(FileItem f) => serverImageProvider(
    widget.client,
    widget.share.id,
    widget.client.fileUrl(widget.share.id, _pathOf(f)),
    f.mtime,
  );

  /// 原图限制解码尺寸，避免 4000 万像素照片占满内存。
  ImageProvider _original(FileItem f) => ResizeImage(
    _originalImage(f),
    width: 4096,
    height: 4096,
    policy: ResizeImagePolicy.fit,
  );

  /// 预取第 [i] 张前后各 2 张的预览图（后 1、前 1、后 2、前 2），取消不再需要的；null 全部取消。
  void _preload(int? i) {
    final w = _previewWidth;
    final want = i == null
        ? const <ServerImage>[]
        : [
            for (final j in [i + 1, i - 1, i + 2, i - 2])
              if (j >= 0 && j < widget.images.length)
                ?_preview(widget.images[j], w),
          ];
    for (final p in _preloads) {
      if (!want.contains(p)) _queue.cancel(p.cacheKey);
    }
    _preloads = want;
    for (final p in want) {
      precacheImage(p, context, onError: (_, _) {});
    }
  }

  Widget _page(int i) {
    final f = widget.images[i];
    final w = _previewWidth;
    final preview = _preview(f, w);
    // 显示中的页（含翻页途中露出的相邻页）在队列里优先，移出后取消其排队中的请求
    final page = _progressive(i, f, w, preview);
    return QueueRetain(
      queue: _queue,
      id: _originalImage(f).cacheKey,
      child: preview == null
          ? page
          : QueueRetain(queue: _queue, id: preview.cacheKey, child: page),
    );
  }

  Widget _progressive(int i, FileItem f, int w, ServerImage? preview) {
    return ProgressiveImage(
      key: ObjectKey(f),
      base: f.hasThumb
          ? cachedThumb(
              widget.client,
              widget.share.id,
              _pathOf(f),
              f.mtime,
              ver: f.thumbVer,
            )
          : null,
      preview: preview,
      previewWidth: w,
      original: _original(f),
      // 当前图显示后再预取相邻图，避免抢占当前图的带宽
      onSettled: () {
        _settled.add(i);
        if (mounted && i == _index) _preload(i);
      },
      onTapUp: () => setState(() => _chrome = !_chrome),
    );
  }

  void _onPage(int i) {
    setState(() => _index = i);
    _preload(_settled.contains(i) ? i : null);
    if (_strip.hasClients) {
      _strip.animateTo(
        (i * _stripCell - 120).clamp(0.0, _strip.position.maxScrollExtent),
        duration: const Duration(milliseconds: 200),
        curve: Curves.easeOut,
      );
    }
  }

  Future<void> _save() async {
    final f = widget.images[_index];
    setState(() => _saving = true);
    final r = await saveImageToPhone(
      perms: widget.share.perms,
      fileName: f.name,
      download: () => widget.client.download(widget.share.id, _pathOf(f)),
    );
    if (!mounted) return;
    setState(() => _saving = false);
    final msg = switch (r) {
      SaveResult.saved => '已保存到相册 $saveAlbum',
      SaveResult.denied => '没有存储权限，请在系统设置中允许',
      SaveResult.forbidden => '此文件夹不允许下载',
      SaveResult.failed => '保存失败，请重试',
    };
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));
  }

  @override
  Widget build(BuildContext context) {
    final f = widget.images[_index];
    return Scaffold(
      backgroundColor: Colors.black,
      body: Stack(
        fit: StackFit.expand,
        children: [
          // 与 PhotoViewGallery 相同：放大后横向拖动先平移图片，到边缘再翻页
          PhotoViewGestureDetectorScope(
            axis: Axis.horizontal,
            child: PageView.builder(
              controller: _pages,
              itemCount: widget.images.length,
              onPageChanged: _onPage,
              itemBuilder: (context, i) => ClipRect(child: _page(i)),
            ),
          ),
          if (_chrome) ...[_top(f), _bottom(f)],
        ],
      ),
    );
  }

  Widget _top(FileItem f) => Positioned(
    left: 0,
    right: 0,
    top: 0,
    child: DecoratedBox(
      decoration: const BoxDecoration(
        gradient: LinearGradient(
          begin: Alignment.topCenter,
          end: Alignment.bottomCenter,
          colors: [Color(0x99000000), Color(0x00000000)],
        ),
      ),
      child: SafeArea(
        bottom: false,
        child: Row(
          children: [
            IconButton(
              tooltip: '返回',
              color: NightColors.moon,
              icon: const Icon(Icons.arrow_back),
              onPressed: () => Navigator.of(context).maybePop(),
            ),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    f.name,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(color: NightColors.moon),
                  ),
                  Text(
                    '${_index + 1} / ${widget.images.length}',
                    style: const TextStyle(
                      color: NightColors.ash,
                      fontSize: 12,
                      fontFeatures: [FontFeature.tabularFigures()],
                    ),
                  ),
                ],
              ),
            ),
            IconButton(
              tooltip: '旋转屏幕',
              color: NightColors.moon,
              icon: const Icon(Icons.screen_rotation),
              onPressed: _rotate,
            ),
          ],
        ),
      ),
    ),
  );

  Widget _bottom(FileItem f) => Positioned(
    left: 0,
    right: 0,
    bottom: 0,
    child: DecoratedBox(
      decoration: const BoxDecoration(
        gradient: LinearGradient(
          begin: Alignment.bottomCenter,
          end: Alignment.topCenter,
          colors: [Color(0xCC000000), Color(0x00000000)],
        ),
      ),
      child: SafeArea(
        top: false,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(16, 24, 16, 8),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                formatTime(f.mtime),
                style: const TextStyle(
                  color: NightColors.moon,
                  fontFeatures: [FontFeature.tabularFigures()],
                ),
              ),
              Text(
                '${formatSize(f.size)} · ${widget.dir.isEmpty ? widget.share.name : widget.dir}',
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(color: NightColors.ash, fontSize: 12),
              ),
              const SizedBox(height: 10),
              SizedBox(
                height: 56,
                child: ListView.builder(
                  controller: _strip,
                  scrollDirection: Axis.horizontal,
                  itemCount: widget.images.length,
                  itemExtent: _stripCell,
                  itemBuilder: (context, i) {
                    final img = widget.images[i];
                    final current = i == _index;
                    return GestureDetector(
                      onTap: () => _pages.jumpToPage(i),
                      child: Container(
                        margin: const EdgeInsets.all(2),
                        decoration: BoxDecoration(
                          border: Border.all(
                            color: current
                                ? NightColors.wick
                                : Colors.transparent,
                            width: 2,
                          ),
                          borderRadius: BorderRadius.circular(6),
                        ),
                        child: ClipRRect(
                          borderRadius: BorderRadius.circular(4),
                          child: img.hasThumb
                              ? ServerThumb(
                                  client: widget.client,
                                  share: widget.share.id,
                                  path: _pathOf(img),
                                  mtime: img.mtime,
                                  width: 56,
                                  placeholder: const ThumbPlaceholder(
                                    icon: Icons.image_outlined,
                                  ),
                                )
                              : const ThumbPlaceholder(
                                  icon: Icons.image_outlined,
                                ),
                        ),
                      ),
                    );
                  },
                ),
              ),
              if (canSaveToPhone(widget.share.perms)) ...[
                const SizedBox(height: 10),
                FilledButton.icon(
                  onPressed: _saving ? null : _save,
                  icon: _saving
                      ? const SizedBox(
                          width: 18,
                          height: 18,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      : const Icon(Icons.download),
                  label: const Text('保存到手机'),
                ),
              ],
            ],
          ),
        ),
      ),
    ),
  );
}
