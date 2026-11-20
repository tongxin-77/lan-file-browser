import 'package:flutter_test/flutter_test.dart';
import 'package:lfb_app/theme.dart';
import 'package:lfb_app/widgets/update_dialog.dart';
import 'package:material_ui/material_ui.dart';

/// 放一个按钮，点击后调用 [open]。
Widget host(void Function(BuildContext) open) => MaterialApp(
  theme: buildNightTheme(),
  home: Builder(
    builder: (context) => Scaffold(
      body: Center(
        child: TextButton(
          onPressed: () => open(context),
          child: const Text('打开'),
        ),
      ),
    ),
  ),
);

void main() {
  testWidgets('强制更新对话框：点外部、返回键都关不掉，没有“稍后”', (tester) async {
    await tester.pumpWidget(
      host(
        (ctx) => showUpdateDialog(
          ctx,
          UpdateOffer(
            version: '2.0.0',
            notes: '接口变更',
            forced: true,
            apkUrl: Uri.parse('http://h/api/app/apk/arm64-v8a'),
            sha256: 'ab',
          ),
        ),
      ),
    );
    await tester.tap(find.text('打开'));
    await tester.pumpAndSettle();
    expect(find.text('需要更新 App'), findsOneWidget);
    expect(find.text('稍后'), findsNothing);
    expect(find.text('更新'), findsOneWidget);

    await tester.tapAt(const Offset(5, 5)); // 点遮罩
    await tester.pumpAndSettle();
    expect(find.text('需要更新 App'), findsOneWidget);

    final nav = tester.state<NavigatorState>(find.byType(Navigator));
    await nav.maybePop(); // 系统返回键
    await tester.pumpAndSettle();
    expect(find.text('需要更新 App'), findsOneWidget);
  });

  testWidgets('普通更新对话框可点“稍后”关闭', (tester) async {
    bool? r;
    await tester.pumpWidget(
      host((ctx) async {
        r = await showUpdateDialog(
          ctx,
          const UpdateOffer(version: '1.3.0', notes: '', forced: false),
        );
      }),
    );
    await tester.tap(find.text('打开'));
    await tester.pumpAndSettle();
    expect(find.text('发现新版本'), findsOneWidget);
    await tester.tap(find.text('稍后'));
    await tester.pumpAndSettle();
    expect(find.text('发现新版本'), findsNothing);
    expect(r, isFalse);
  });
}
