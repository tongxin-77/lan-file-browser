import 'package:flutter_test/flutter_test.dart';
import 'package:lfb_app/theme.dart';
import 'package:lfb_app/widgets/password_sheet.dart';
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
  testWidgets('密码不对：清空输入、面板保留、不出现任何文字提示', (tester) async {
    final tried = <String>[];
    bool? result;
    await tester.pumpWidget(
      host((ctx) async {
        result = await showPasswordSheet(ctx, (pw) async {
          tried.add(pw);
          return pw == '0000';
        });
      }),
    );
    await tester.tap(find.text('打开'));
    await tester.pumpAndSettle();
    expect(find.text('输入密码'), findsOneWidget);
    final texts = tester
        .widgetList<Text>(find.byType(Text))
        .map((t) => t.data)
        .toSet();

    await tester.enterText(find.byKey(const Key('vault-password')), '1234');
    await tester.tap(find.text('确定'));
    await tester.pumpAndSettle();
    expect(tried, ['1234']);
    expect(find.text('输入密码'), findsOneWidget, reason: '面板保留可重输');
    final field = tester.widget<TextField>(
      find.byKey(const Key('vault-password')),
    );
    expect(field.controller!.text, isEmpty);
    // 与错误前相比，界面上没有多出任何文字
    expect(
      tester.widgetList<Text>(find.byType(Text)).map((t) => t.data).toSet(),
      texts,
    );
    expect(find.textContaining('错误'), findsNothing);

    // 不足 4 位不请求服务端，同样清空
    await tester.enterText(find.byKey(const Key('vault-password')), '12');
    await tester.tap(find.text('确定'));
    await tester.pumpAndSettle();
    expect(tried, ['1234']);

    await tester.enterText(find.byKey(const Key('vault-password')), '0000');
    await tester.tap(find.text('确定'));
    await tester.pumpAndSettle();
    expect(find.text('输入密码'), findsNothing);
    expect(result, isTrue);
  });

  testWidgets('数字 / 字母键盘可切换', (tester) async {
    await tester.pumpWidget(
      host((ctx) => showPasswordSheet(ctx, (_) async => false)),
    );
    await tester.tap(find.text('打开'));
    await tester.pumpAndSettle();
    TextField field() =>
        tester.widget<TextField>(find.byKey(const Key('vault-password')));
    expect(field().keyboardType, TextInputType.number);
    await tester.tap(find.text('ABC'));
    await tester.pump();
    expect(field().keyboardType, TextInputType.visiblePassword);
    expect(find.text('123'), findsOneWidget);
  });
}
