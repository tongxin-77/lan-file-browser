import 'package:material_ui/material_ui.dart';

import 'api/lfb_client.dart';
import 'models/server.dart';
import 'pages/connect_page.dart';
import 'pages/home_page.dart';
import 'pages/startup_page.dart';
import 'services/server_store.dart';
import 'services/update_checker.dart';
import 'theme.dart';

final navigatorKey = GlobalKey<NavigatorState>();
final serverStore = ServerStore();

final updateChecker = UpdateChecker(() => navigatorKey.currentContext);

class LfbApp extends StatelessWidget {
  const LfbApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'LAN File Browser',
      navigatorKey: navigatorKey,
      theme: buildNightTheme(),
      debugShowCheckedModeBanner: false,
      home: const StartupPage(),
    );
  }
}

/// 已配对服务器的客户端：任何带 token 的请求 401 → 清 token 回连接页（01 §3.3）。
LfbClient clientFor(SavedServer server, String token) => LfbClient(
  host: server.lastIp,
  port: server.port,
  token: token,
  serverId: server.serverId,
  onUnauthorized: () => _signOut(server),
);

bool _signingOut = false;

Future<void> _signOut(SavedServer server) async {
  if (_signingOut) return;
  _signingOut = true;
  try {
    await serverStore.clearToken(server.serverId);
    showConnectPage(message: '「${server.name}」的授权已失效，请重新配对');
  } finally {
    _signingOut = false;
  }
}

void openHome(SavedServer server, String token) {
  final client = clientFor(server, token);
  navigatorKey.currentState?.pushAndRemoveUntil(
    MaterialPageRoute<void>(
      builder: (_) => HomePage(server: server, client: client),
    ),
    (_) => false,
  );
  updateChecker.start(client); // 连接成功后检查更新与电脑端版本（02 §5）
}

void showConnectPage({String? message, bool canRetry = false}) {
  navigatorKey.currentState?.pushAndRemoveUntil(
    MaterialPageRoute<void>(
      builder: (_) => ConnectPage(message: message, canRetry: canRetry),
    ),
    (_) => false,
  );
}
