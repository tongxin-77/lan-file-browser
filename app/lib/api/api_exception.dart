/// App 内所有接口错误的基类；[message] 可直接展示给用户。
sealed class LfbException implements Exception {
  const LfbException(this.message);
  final String message;

  @override
  String toString() => message;
}

/// 服务端返回的错误：`{"error":{"code":"...","message":"..."}}`（03 §7）。
class ApiException extends LfbException {
  const ApiException(this.statusCode, this.code, String message)
    : super(message);

  final int statusCode;

  /// UNAUTHORIZED / NOT_FOUND / FORBIDDEN / BAD_PATH / SHARE_OFFLINE / LOCKED …；
  /// 响应体不是统一错误格式时为 `HTTP_<状态码>`。
  final String code;

  bool get isUnauthorized => statusCode == 401;
}

/// 网络不可达、超时、连接中断等。
class NetworkException extends LfbException {
  const NetworkException([super.message = '无法连接到电脑，请确认电脑已开机且在同一网络']);
}
