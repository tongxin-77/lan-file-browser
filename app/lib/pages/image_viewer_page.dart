import 'package:cached_network_image/cached_network_image.dart';
import 'package:material_ui/material_ui.dart';
import 'package:photo_view/photo_view.dart';
import 'package:photo_view/photo_view_gallery.dart';

import '../api/lfb_client.dart';
import '../models/file_item.dart';
import '../models/share.dart';
import '../services/image_saver.dart';
import '../services/thumbs.dart';
import '../theme.dart';
import '../widgets/file_tile.dart';
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

  static const _stripCell = 60.0;

  @override
  void dispose() {
    _pages.dispose();
    _strip.dispose();
    super.dispose();
  }

  String _pathOf(FileItem f) => joinPath(widget.dir, f.name);

  ImageProvider _provider(FileItem f) {
    final url = widget.client.fileUrl(widget.share.id, _pathOf(f));
    // 大图限制解码尺寸，避免 4000 万像素照片占满内存
    return ResizeImage(
      CachedNetworkImageProvider(
        url.toString(),
        cacheKey: imageCacheKey(url, f.mtime),
      ),
      width: 2560,
      height: 2560,
      policy: ResizeImagePolicy.fit,
    );
  }

  void _onPage(int i) {
    setState(() => _index = i);
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
          PhotoViewGallery.builder(
            pageController: _pages,
            itemCount: widget.images.length,
            onPageChanged: _onPage,
            builder: (context, i) => PhotoViewGalleryPageOptions(
              imageProvider: _provider(widget.images[i]),
              minScale: PhotoViewComputedScale.contained,
              maxScale: PhotoViewComputedScale.covered * 4,
              onTapUp: (_, _, _) => setState(() => _chrome = !_chrome),
              errorBuilder: (_, _, _) => const Center(
                child: Text('图片无法显示', style: TextStyle(color: NightColors.ash)),
              ),
            ),
            loadingBuilder: (_, _) =>
                const Center(child: CircularProgressIndicator()),
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
