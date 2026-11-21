import 'package:material_ui/material_ui.dart';

import 'api/lfb_client.dart';
import 'models/server.dart';
import 'pages/connect_page.dart';
import 'pages/home_page.dart';
import 'pages/startup_page.dart';
import 'services/server_store.dart';
import 'services/update_checker.dart';
import 'services/vault.dart';
import 'theme.dart';

final navigatorKey = GlobalKey<NavigatorState>();
final serverStore = ServerStore();

/// 加密片库会话（仅内存）；上锁时清空图片内存缓存并回到首页（04 §8）。
final vaultController = VaultController()..onLocked = _onVaultLocked;
final updateChecker = UpdateChecker(() => navigatorKey.currentContext);

void _onVaultLocked() {
  PaintingBinding.instance.imageCache
    ..clear()
    ..clearLiveImages();
  navigatorKey.currentState?.popUntil((r) => r.isFirst);
}

class LfbApp extends StatelessWidget {
  const LfbApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'LAN File Browser',
      navigatorKey: navigatorKey,
      theme: buildNightTheme(),
      debugShowCheckedModeBanner: false,
      // 任何触摸都算“有操作”，重置片库空闲计时
      builder: (context, child) => Listener(
        behavior: HitTestBehavior.translucent,
        onPointerDown: (_) => vaultController.touch(),
        child: child,
      ),
      home: const StartupPage(),
    );
  }
}

/// 已配对服务器的客户端：任何带 token 的请求 401 → 清 token 回连接页（01 §3.3）；
/// 断线重连会尝试全部已知地址，换地址后保存（01 §6）。
LfbClient clientFor(SavedServer server, String token) => LfbClient(
  host: server.lastIp,
  port: server.port,
  token: token,
  serverId: server.serverId,
  fallbackHosts: server.ips,
  onUnauthorized: () => _signOut(server),
  onHostChanged: (ip) => serverStore.save(server.withAddress(ip)),
);

bool _signingOut = false;

Future<void> _signOut(SavedServer server) async {
  if (_signingOut) return;
  _signingOut = true;
  try {
    await serverStore.clearToken(server.serverId);
    showConnectPage(message: '这台电脑（${server.name}）已将本机移除，请重新配对');
  } finally {
    _signingOut = false;
  }
}

void openHome(SavedServer server, String token) {
  vaultController.lock();
  final client = clientFor(server, token);
  navigatorKey.currentState?.pushAndRemoveUntil(
    MaterialPageRoute<void>(
      builder: (_) => HomePage(server: server, client: client),
    ),
    (_) => false,
  );
  updateChecker.start(client); // 连接成功后检查更新与电脑端版本（02 §5）
}

void showConnectPage({String? message}) {
  vaultController.lock();
  navigatorKey.currentState?.pushAndRemoveUntil(
    MaterialPageRoute<void>(builder: (_) => ConnectPage(message: message)),
    (_) => false,
  );
}
