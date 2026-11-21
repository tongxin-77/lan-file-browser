import 'package:material_ui/material_ui.dart';

import '../api/api_exception.dart';
import '../app.dart';
import '../services/platform.dart';

/// 启动：有已保存服务器则 ping 上次地址，成功进首页，失败回连接页提示（01 §6；
/// UDP 按 serverId 重新发现后期实现）。
class StartupPage extends StatefulWidget {
  const StartupPage({super.key});

  @override
  State<StartupPage> createState() => _StartupPageState();
}

class _StartupPageState extends State<StartupPage> {
  @override
  void initState() {
    super.initState();
    _start();
  }

  Future<void> _start() async {
    final last = await serverStore.lastServer();
    if (last == null) {
      showConnectPage();
      return;
    }
    final (server, token) = last;
    try {
      await clientFor(
        server,
        token,
      ).verify(timeout: const Duration(seconds: 3));
      openHome(server, token);
    } on ApiException catch (e) {
      // 401 已由 onUnauthorized 跳转连接页
      if (!e.isUnauthorized) {
        showConnectPage(message: e.message, canRetry: true);
      }
    } on LfbException {
      showConnectPage(
        message: await connectionErrorText(
          '连不上「${server.name}」（${server.lastIp}:${server.port}）。'
          '请确认电脑已开机且在同一网络，或重新扫码。',
        ),
        canRetry: true,
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return const Scaffold(body: Center(child: CircularProgressIndicator()));
  }
}
