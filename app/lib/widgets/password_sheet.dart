import 'package:flutter/services.dart';
import 'package:material_ui/material_ui.dart';

import '../theme.dart';

/// 首页长按电脑名称弹出的“输入密码”面板（a4 设计 2a，docs/04-vault.md §4）。
///
/// 用系统键盘，右侧可在数字 / 字母间切换；[onSubmit] 返回 false（含网络错误）时
/// 只清空并轻微左右抖动，不出现任何文字提示。成功时面板以 true 关闭。
Future<bool> showPasswordSheet(
  BuildContext context,
  Future<bool> Function(String password) onSubmit,
) async {
  final ok = await showModalBottomSheet<bool>(
    context: context,
    isScrollControlled: true,
    backgroundColor: Colors.transparent,
    elevation: 0,
    builder: (_) => PasswordSheet(onSubmit: onSubmit),
  );
  return ok ?? false;
}

class PasswordSheet extends StatefulWidget {
  const PasswordSheet({super.key, required this.onSubmit});

  final Future<bool> Function(String password) onSubmit;

  @override
  State<PasswordSheet> createState() => _PasswordSheetState();
}

class _PasswordSheetState extends State<PasswordSheet>
    with SingleTickerProviderStateMixin {
  final _text = TextEditingController();
  late final _shake = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 360),
  );
  // 设计稿 @keyframes shake：-7 → 6 → -3 → 0
  late final _dx = TweenSequence<double>([
    TweenSequenceItem(tween: Tween(begin: 0, end: -7), weight: 20),
    TweenSequenceItem(tween: Tween(begin: -7, end: 6), weight: 25),
    TweenSequenceItem(tween: Tween(begin: 6, end: -3), weight: 25),
    TweenSequenceItem(tween: Tween(begin: -3, end: 0), weight: 30),
  ]).animate(CurvedAnimation(parent: _shake, curve: Curves.ease));
  bool _letters = false;
  bool _busy = false;

  @override
  void dispose() {
    _text.dispose();
    _shake.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (_busy) return;
    final pw = _text.text;
    var ok = false;
    if (pw.length >= 4) {
      setState(() => _busy = true);
      try {
        ok = await widget.onSubmit(pw);
      } on Object {
        ok = false;
      }
      if (!mounted) return;
      setState(() => _busy = false);
    }
    if (ok) {
      Navigator.of(context).pop(true);
      return;
    }
    _text.clear();
    _shake.forward(from: 0);
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: EdgeInsets.fromLTRB(
        12,
        0,
        12,
        34 + MediaQuery.viewInsetsOf(context).bottom,
      ),
      child: AnimatedBuilder(
        animation: _dx,
        builder: (context, child) =>
            Transform.translate(offset: Offset(_dx.value, 0), child: child),
        child: DecoratedBox(
          decoration: BoxDecoration(
            color: NightColors.pillow,
            borderRadius: BorderRadius.circular(28),
            boxShadow: [
              BoxShadow(
                color: NightColors.wick.withValues(alpha: 0.28),
                blurRadius: 60,
                spreadRadius: -10,
              ),
            ],
          ),
          child: Padding(
            padding: const EdgeInsets.fromLTRB(16, 22, 16, 16),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Padding(
                  padding: const EdgeInsets.symmetric(horizontal: 6),
                  child: Text(
                    '输入密码',
                    style: Theme.of(context).textTheme.headlineSmall
                        ?.copyWith(fontWeight: FontWeight.w300),
                  ),
                ),
                const SizedBox(height: 14),
                TextField(
                  key: const Key('vault-password'),
                  controller: _text,
                  autofocus: true,
                  obscureText: true,
                  enableSuggestions: false,
                  autocorrect: false,
                  readOnly: _busy,
                  keyboardType: _letters
                      ? TextInputType.visiblePassword
                      : TextInputType.number,
                  inputFormatters: [LengthLimitingTextInputFormatter(16)],
                  textInputAction: TextInputAction.done,
                  onSubmitted: (_) => _submit(),
                  decoration: InputDecoration(
                    fillColor: NightColors.quilt,
                    border: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(16),
                      borderSide: const BorderSide(color: NightColors.seam),
                    ),
                    enabledBorder: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(16),
                      borderSide: const BorderSide(color: NightColors.seam),
                    ),
                    suffixIcon: TextButton(
                      onPressed: () => setState(() => _letters = !_letters),
                      style: TextButton.styleFrom(
                        foregroundColor: NightColors.ash,
                        minimumSize: const Size(56, 48),
                      ),
                      child: Text(_letters ? '123' : 'ABC'),
                    ),
                  ),
                ),
                const SizedBox(height: 14),
                Row(
                  children: [
                    Expanded(
                      child: TextButton(
                        onPressed: () => Navigator.of(context).pop(false),
                        style: TextButton.styleFrom(
                          minimumSize: const Size(48, 52),
                          foregroundColor: NightColors.moon,
                        ),
                        child: const Text('取消'),
                      ),
                    ),
                    const SizedBox(width: 10),
                    Expanded(
                      child: FilledButton(
                        onPressed: _busy ? null : _submit,
                        child: const Text('确定'),
                      ),
                    ),
                  ],
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
