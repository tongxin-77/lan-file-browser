import 'package:flutter_test/flutter_test.dart';
import 'package:lfb_app/models/connect_uri.dart';

void main() {
  group('ConnectUri.tryParse', () {
    test('解析完整二维码', () {
      final c = ConnectUri.tryParse(
        'lfb://connect?id=abc-123&name=%E5%AE%A2%E5%8E%85PC&port=8686'
        '&ips=192.168.1.5,10.0.0.3&code=xyz',
      )!;
      expect(c.serverId, 'abc-123');
      expect(c.name, '客厅PC');
      expect(c.port, 8686);
      expect(c.ips, ['192.168.1.5', '10.0.0.3']);
      expect(c.code, 'xyz');
    });

    test('忽略空 IP 段与首尾空白', () {
      final c = ConnectUri.tryParse(
        '  lfb://connect?id=a&port=9000&ips=1.2.3.4,,&code=c  ',
      )!;
      expect(c.ips, ['1.2.3.4']);
      expect(c.port, 9000);
    });

    for (final bad in [
      'https://connect?id=a&port=8686&ips=1.2.3.4&code=c', // scheme 不对
      'lfb://other?id=a&port=8686&ips=1.2.3.4&code=c', // host 不对
      'lfb://connect?port=8686&ips=1.2.3.4&code=c', // 缺 id
      'lfb://connect?id=a&port=8686&ips=1.2.3.4', // 缺 code
      'lfb://connect?id=a&port=8686&ips=&code=c', // 无 IP
      'lfb://connect?id=a&port=0&ips=1.2.3.4&code=c', // 端口非法
      'lfb://connect?id=a&port=x&ips=1.2.3.4&code=c',
      '随便一段文字',
    ]) {
      test('拒绝 $bad', () => expect(ConnectUri.tryParse(bad), isNull));
    }
  });

  group('parseHostPort', () {
    test('缺省端口 8686', () {
      expect(parseHostPort('192.168.1.5'), ('192.168.1.5', 8686));
    });
    test('带端口', () {
      expect(parseHostPort(' 10.0.0.3:9000 '), ('10.0.0.3', 9000));
    });
    test('非法输入', () {
      for (final s in ['', ':8686', '1.2.3.4:', '1.2.3.4:70000', 'http://x']) {
        expect(parseHostPort(s), isNull, reason: s);
      }
    });
  });
}
