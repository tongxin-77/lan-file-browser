import 'dart:io';

import 'package:material_ui/material_ui.dart';
import 'package:path_provider/path_provider.dart';

import '../services/app_update.dart';
import '../services/platform.dart';
import '../theme.dart';

/// 一次更新提示的内容。[apkUrl] 为 null 表示服务端暂无适合本机的安装包。
class UpdateOffer {
  const UpdateOffer({
    required this.version,
    required this.notes,
    required this.forced,
    this.apkUrl,
    this.sha256,
    this.headers = const {},
  });

  final String? version;
  final String notes;
  final bool forced;
  final Uri? apkUrl;
  final String? sha256;

  /// 下载 APK 时附带的请求头（设备 Bearer token）。
  final Map<String, String> headers;
}

/// App 更新对话框（02 §5.2）：普通更新可关闭；强制更新不可关闭、不可返回。
/// 返回 true 表示已交给系统安装器。
Future<bool> showUpdateDialog(BuildContext context, UpdateOffer offer) async {
  final r = await showDialog<bool>(
    context: context,
    barrierDismissible: !offer.forced,
    builder: (_) => UpdateDialog(offer: offer),
  );
  return r ?? false;
}

enum _Stage { offer, downloading, failed, install }

class UpdateDialog extends StatefulWidget {
  const UpdateDialog({super.key, required this.offer});

  final UpdateOffer offer;

  @override
  State<UpdateDialog> createState() => _UpdateDialogState();
}

class _UpdateDialogState extends State<UpdateDialog> {
  _Stage _stage = _Stage.offer;
  double? _progress;
  String? _error;
  ApkDownload? _download;
  File? _file;
  bool _needPermission = false;

  @override
  void dispose() {
    _download?.cancel();
    super.dispose();
  }

  Future<void> _start() async {
    final url = widget.offer.apkUrl!;
    setState(() {
      _stage = _Stage.downloading;
      _progress = null;
      _error = null;
    });
    try {
      final dir = Directory(
        '${(await getApplicationCacheDirectory()).path}/apk',
      );
      if (dir.existsSync()) dir.deleteSync(recursive: true); // 只留本次下载
      final dl = _download = ApkDownload.start(
        url,
        File('${dir.path}/lfb-app-${widget.offer.version}.apk'),
        headers: widget.offer.headers,
        onProgress: (got, total) {
          if (mounted && total != null && total > 0) {
            setState(() => _progress = got / total);
          }
        },
      );
      final file = await dl.done;
      if (!await verifySha256(file, widget.offer.sha256 ?? '')) {
        file.deleteSync();
        throw const FileSystemException('校验失败');
      }
      _file = file;
      if (!mounted) return;
      setState(() => _stage = _Stage.install);
      await _install();
    } on DownloadCancelled {
      if (mounted) setState(() => _stage = _Stage.offer);
    } on Object catch (e) {
      if (!mounted) return;
      setState(() {
        _stage = _Stage.failed;
        _error = e is FileSystemException && e.message == '校验失败'
            ? '安装包校验失败，请重试'
            : '下载失败，请确认与电脑的连接后重试';
      });
    } finally {
      _download = null;
    }
  }

  /// 首次需用户授予“安装未知应用”；已授予则直接调起系统安装器。
  Future<void> _install() async {
    if (!await PlatformService.canInstallPackages()) {
      if (mounted) setState(() => _needPermission = true);
      return;
    }
    if (mounted) setState(() => _needPermission = false);
    final ok = await PlatformService.installApk(_file!.path);
    if (ok && mounted && !widget.offer.forced) Navigator.of(context).pop(true);
  }

  @override
  Widget build(BuildContext context) {
    final o = widget.offer;
    return PopScope(
      canPop: !o.forced && _stage != _Stage.downloading,
      child: AlertDialog(
        backgroundColor: NightColors.pillow,
        title: Text(o.forced ? '需要更新 App' : '发现新版本'),
        content: SingleChildScrollView(child: _content(o)),
        actions: _actions(o),
      ),
    );
  }

  Widget _content(UpdateOffer o) {
    const faint = TextStyle(color: NightColors.ash, height: 1.5);
    final lines = <Widget>[
      if (o.version != null) Text('新版本 ${o.version}'),
      if (o.forced) const Text('当前版本过旧，无法继续与这台电脑配合使用，请更新后再用。', style: faint),
      if (o.notes.trim().isNotEmpty) ...[
        const SizedBox(height: 8),
        Text(o.notes.trim(), style: faint),
      ],
      if (o.apkUrl == null)
        const Text('电脑上暂时没有适合这台手机的安装包，请稍后再试。', style: faint),
    ];
    switch (_stage) {
      case _Stage.downloading:
        lines.addAll([
          const SizedBox(height: 16),
          LinearProgressIndicator(value: _progress),
          const SizedBox(height: 8),
          Text(
            _progress == null ? '正在下载…' : '正在下载 ${(_progress! * 100).floor()}%',
            style: faint,
          ),
        ]);
      case _Stage.failed:
        lines.addAll([const SizedBox(height: 16), Text(_error ?? '')]);
      case _Stage.install:
        lines.addAll([
          const SizedBox(height: 16),
          Text(
            _needPermission
                ? '请在设置中允许本应用“安装未知应用”，返回后点“安装”。'
                : '已下载并校验，正在打开系统安装程序。',
            style: faint,
          ),
        ]);
      case _Stage.offer:
        break;
    }
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: lines,
    );
  }

  List<Widget> _actions(UpdateOffer o) {
    final later = o.forced
        ? null
        : TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('稍后'),
          );
    switch (_stage) {
      case _Stage.offer:
      case _Stage.failed:
        return [
          ?later,
          if (o.apkUrl != null)
            FilledButton(
              onPressed: _start,
              child: Text(_stage == _Stage.failed ? '重试' : '更新'),
            ),
        ];
      case _Stage.downloading:
        return [
          TextButton(
            onPressed: () => _download?.cancel(),
            child: const Text('取消'),
          ),
        ];
      case _Stage.install:
        return [
          ?later,
          if (_needPermission)
            TextButton(
              onPressed: PlatformService.openInstallSettings,
              child: const Text('去设置'),
            ),
          FilledButton(onPressed: _install, child: const Text('安装')),
        ];
    }
  }
}

/// 电脑端版本过旧（低于 App 的 minServerVersion，02 §5.3）。
Future<void> showServerUpgradeDialog(BuildContext context, String version) =>
    showDialog<void>(
      context: context,
      builder: (context) => AlertDialog(
        backgroundColor: NightColors.pillow,
        title: const Text('请升级电脑端'),
        content: SelectableText(
          '电脑上的 LAN File Browser（$version）版本过旧，部分功能可能无法使用。'
          '请在电脑上下载安装新版：\n$serverReleaseUrl',
          style: const TextStyle(color: NightColors.ash, height: 1.5),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(),
            child: const Text('知道了'),
          ),
        ],
      ),
    );

const serverReleaseUrl =
    'https://github.com/tongxin-77/lan-file-browser/releases/latest';
