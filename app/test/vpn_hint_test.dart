import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:lfb_app/services/platform.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const channel = MethodChannel('com.lanfilebrowser/platform');
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;

  void mockVpn(Object? Function() reply) {
    messenger.setMockMethodCallHandler(channel, (call) async {
      expect(call.method, 'isVpnActive');
      return reply();
    });
  }

  tearDown(() => messenger.setMockMethodCallHandler(channel, null));

  test('withVpnHint：开 VPN 时另起一行追加提示，否则原样', () {
    expect(withVpnHint('连接超时', vpn: false), '连接超时');
    expect(withVpnHint('连接超时', vpn: true), '连接超时\n$vpnHint');
    expect(vpnHint, contains('绕过局域网/私有网络'));
    expect(vpnHint, contains('LAN File Browser'));
  });

  test('connectionErrorText：原生报告 VPN 开启时追加提示', () async {
    mockVpn(() => true);
    expect(await connectionErrorText('连不上'), '连不上\n$vpnHint');
  });

  test('connectionErrorText：无 VPN 时不追加', () async {
    mockVpn(() => false);
    expect(await connectionErrorText('连不上'), '连不上');
  });

  test('connectionErrorText：原生调用出错时按无 VPN 处理', () async {
    mockVpn(() => throw PlatformException(code: 'ERR'));
    expect(await connectionErrorText('连不上'), '连不上');
  });

  test('connectionErrorText：未注册通道（非 Android）时按无 VPN 处理', () async {
    expect(await connectionErrorText('连不上'), '连不上');
  });
}
