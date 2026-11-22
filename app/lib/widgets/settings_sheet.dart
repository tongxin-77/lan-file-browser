import 'package:material_ui/material_ui.dart';

import '../services/app_update.dart';
import '../services/platform.dart';
import '../theme.dart';

/// 首页右上角“设置”面板：当前 App 版本 + 手动“检查更新”（02 §5.2）。
///
/// [onCheck] 有新版时自行弹更新框并返回 null，否则返回要显示的结果；
/// 失败时另给“在浏览器中打开下载页”。
Future<void> showSettingsSheet(
  BuildContext context, {
  required Future<String> version,
  required Future<({String text, bool failed})?> Function() onCheck,
}) => showModalBottomSheet<void>(
  context: context,
  isScrollControlled: true,
  backgroundColor: Colors.transparent,
  elevation: 0,
  builder: (_) => SettingsSheet(version: version, onCheck: onCheck),
);

class SettingsSheet extends StatefulWidget {
  const SettingsSheet({
    super.key,
    required this.version,
    required this.onCheck,
  });

  final Future<String> version;
  final Future<({String text, bool failed})?> Function() onCheck;

  @override
  State<SettingsSheet> createState() => _SettingsSheetState();
}

class _SettingsSheetState extends State<SettingsSheet> {
  bool _checking = false;
  ({String text, bool failed})? _result;

  Future<void> _check() async {
    setState(() {
      _checking = true;
      _result = null;
    });
    final r = await widget.onCheck();
    if (!mounted) return;
    setState(() {
      _checking = false;
      _result = r;
    });
  }

  @override
  Widget build(BuildContext context) {
    const faint = TextStyle(color: NightColors.ash, height: 1.4);
    return SingleChildScrollView(
      padding: const EdgeInsets.fromLTRB(12, 0, 12, 34),
      child: Material(
        color: NightColors.pillow,
        borderRadius: BorderRadius.circular(28),
        clipBehavior: Clip.antiAlias,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(8, 22, 8, 8),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 16),
                child: Text(
                  '设置',
                  style: Theme.of(context).textTheme.headlineSmall
                      ?.copyWith(fontWeight: FontWeight.w300),
                ),
              ),
              const SizedBox(height: 8),
              ListTile(
                title: const Text('App 版本'),
                trailing: FutureBuilder<String>(
                  future: widget.version,
                  builder: (context, s) =>
                      Text(s.data ?? (s.hasError ? '未知' : '…'), style: faint),
                ),
              ),
              ListTile(
                title: const Text('检查更新'),
                subtitle: _result == null
                    ? null
                    : Text(_result!.text, style: faint),
                trailing: _checking
                    ? const SizedBox.square(
                        dimension: 20,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Icon(Icons.chevron_right, color: NightColors.ash),
                onTap: _checking ? null : _check,
              ),
              if (_result?.failed ?? false)
                Align(
                  alignment: Alignment.centerLeft,
                  child: TextButton(
                    onPressed: () => PlatformService.openUrl(releasesPageUrl),
                    child: const Text('在浏览器中打开下载页'),
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }
}
