import 'dart:async';

import 'package:material_ui/material_ui.dart';
import 'package:photo_view/photo_view.dart';

import '../services/thumbs.dart';
import '../theme.dart';

/// 看图单页的渐进加载：已缓存的列表缩略图 [base] 立即作底图 → 预览图 [preview] 到达后淡入替换
/// → 放大到预览图像素不够时再加载 [original] 并淡入替换。加载中顶部显示细进度条，不遮挡图片。
class ProgressiveImage extends StatefulWidget {
  const ProgressiveImage({
    super.key,
    this.base,
    this.preview,
    this.previewWidth = 0,
    required this.original,
    this.onSettled,
    this.onTapUp,
  });

  /// 已缓存的缩略图（没有则结果为 null）。
  final Future<ImageProvider?>? base;

  /// 预览图；null 表示服务端不能生成（直接加载原图）。
  final ImageProvider? preview;

  /// 预览图请求的档位（判断预览图是否被缩小过）。
  final int previewWidth;
  final ImageProvider original;

  /// 预览图（无预览图时为原图）已显示或失败：可以开始预取相邻图片。
  final VoidCallback? onSettled;
  final VoidCallback? onTapUp;

  @override
  State<ProgressiveImage> createState() => _ProgressiveImageState();
}

class _ProgressiveImageState extends State<ProgressiveImage> {
  final _controller = PhotoViewController();
  late final StreamSubscription<PhotoViewControllerValue> _scaleSub;
  final _listening = <(ImageStream, ImageStreamListener)>[];

  ImageProvider? _base;
  Size? _baseSize, _previewSize, _originalSize;
  bool _previewFailed = false, _originalFailed = false;
  late bool _wantOriginal = widget.preview == null;
  Size? _fitted; // 图片在屏幕内完整显示（缩放 1）时的逻辑尺寸
  bool _initializing = true; // 内存缓存命中时监听会同步回调，此时不能 setState

  @override
  void initState() {
    super.initState();
    _scaleSub = _controller.outputStateStream.listen(_onScale);
    widget.base?.then((p) {
      if (!mounted || p == null) return;
      setState(() => _base = p);
      _watch(p, (s) => _baseSize = s);
    });
    final preview = widget.preview;
    if (preview != null) {
      _watch(preview, (s) {
        _previewSize = s;
        if (s == null) {
          _previewFailed = true;
          _loadOriginal();
        }
        widget.onSettled?.call();
      });
    } else {
      _loadOriginal();
    }
    _initializing = false;
  }

  @override
  void dispose() {
    _scaleSub.cancel();
    for (final (stream, l) in _listening) {
      stream.removeListener(l);
    }
    _listening.clear();
    _controller.dispose();
    super.dispose();
  }

  /// 监听 [p] 的首帧尺寸（失败时以 null 回调）。
  void _watch(ImageProvider p, void Function(Size? size) done) {
    final stream = p.resolve(ImageConfiguration.empty);
    late final (ImageStream, ImageStreamListener) entry;
    void finish(Size? s) {
      _listening.remove(entry);
      stream.removeListener(entry.$2);
      if (_initializing) {
        done(s);
      } else if (mounted) {
        setState(() => done(s));
      }
    }

    entry = (
      stream,
      ImageStreamListener((info, _) {
        final s = Size(
          info.image.width.toDouble(),
          info.image.height.toDouble(),
        );
        info.dispose();
        finish(s);
      }, onError: (_, _) => finish(null)),
    );
    _listening.add(entry);
    stream.addListener(entry.$2);
  }

  void _loadOriginal() {
    _wantOriginal = true;
    _watch(widget.original, (s) {
      _originalSize = s;
      _originalFailed = s == null;
      if (widget.preview == null) widget.onSettled?.call();
    });
  }

  void _onScale(PhotoViewControllerValue v) {
    final preview = _previewSize, fitted = _fitted, scale = v.scale;
    if (_wantOriginal || preview == null || fitted == null || scale == null) {
      return;
    }
    if (needsOriginal(
      displayedLogicalWidth: fitted.width,
      scale: scale,
      devicePixelRatio: MediaQuery.devicePixelRatioOf(context),
      previewPixels: preview,
      requestedWidth: widget.previewWidth,
    )) {
      setState(_loadOriginal);
    }
  }

  @override
  Widget build(BuildContext context) {
    final preview = widget.preview;
    final shown = _originalSize ?? _previewSize ?? _baseSize;
    final failed =
        _originalFailed && (preview == null || _previewFailed) && _base == null;
    final loading =
        !failed &&
        ((preview != null && _previewSize == null && !_previewFailed) ||
            (_wantOriginal && _originalSize == null && !_originalFailed));
    return LayoutBuilder(
      builder: (context, c) {
        final viewport = c.biggest;
        _fitted = shown == null
            ? null
            : applyBoxFit(BoxFit.contain, shown, viewport).destination;
        return Stack(
          fit: StackFit.expand,
          children: [
            PhotoView.customChild(
              controller: _controller,
              childSize: _fitted,
              minScale: PhotoViewComputedScale.contained,
              maxScale: PhotoViewComputedScale.covered * 4,
              initialScale: PhotoViewComputedScale.contained,
              onTapUp: (_, _, _) => widget.onTapUp?.call(),
              child: Stack(
                fit: StackFit.expand,
                children: [
                  if (_base != null) _layer(_base!, fade: false),
                  if (preview != null) _layer(preview),
                  if (_wantOriginal) _layer(widget.original),
                ],
              ),
            ),
            if (failed)
              const Center(
                child: Text('图片无法显示', style: TextStyle(color: NightColors.ash)),
              )
            else if (loading && shown == null)
              const Center(child: CircularProgressIndicator())
            else if (loading)
              const Align(
                alignment: Alignment.topCenter,
                child: SafeArea(child: LinearProgressIndicator(minHeight: 2)),
              ),
          ],
        );
      },
    );
  }

  Widget _layer(ImageProvider p, {bool fade = true}) => Image(
    image: p,
    fit: BoxFit.contain,
    gaplessPlayback: true,
    frameBuilder: (_, child, frame, sync) => sync || !fade
        ? child
        : AnimatedOpacity(
            opacity: frame == null ? 0 : 1,
            duration: const Duration(milliseconds: 200),
            child: child,
          ),
    errorBuilder: (_, _, _) => const SizedBox.shrink(),
  );
}
