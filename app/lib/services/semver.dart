/// 语义化版本比较（与服务端 internal/update/semver.go 规则一致）：
/// `MAJOR.MINOR.PATCH[-pre][+build]`，可带 `v` 前缀，忽略 build；
/// a<b 返回 -1，相等 0，a>b 返回 1；任一无效（如 `dev`）返回 null。
int? compareVersions(String a, String b) {
  final va = _parse(a), vb = _parse(b);
  if (va == null || vb == null) return null;
  for (var i = 0; i < 3; i++) {
    if (va.nums[i] != vb.nums[i]) return va.nums[i] < vb.nums[i] ? -1 : 1;
  }
  // 有预发布标识的版本低于正式版
  if (va.pre.isEmpty || vb.pre.isEmpty) {
    if (va.pre.isEmpty && vb.pre.isEmpty) return 0;
    return va.pre.isEmpty ? 1 : -1;
  }
  for (var i = 0; i < va.pre.length && i < vb.pre.length; i++) {
    final x = va.pre[i], y = vb.pre[i];
    if (x == y) continue;
    final nx = int.tryParse(x), ny = int.tryParse(y);
    if (nx != null && ny != null) return nx < ny ? -1 : 1;
    if (nx != null) return -1; // 数字标识低于字母标识
    if (ny != null) return 1;
    return x.compareTo(y) < 0 ? -1 : 1;
  }
  if (va.pre.length == vb.pre.length) return 0;
  return va.pre.length < vb.pre.length ? -1 : 1;
}

final _ident = RegExp(r'^[0-9A-Za-z-]+$');

({List<int> nums, List<String> pre})? _parse(String s) {
  var core = s.startsWith('v') ? s.substring(1) : s;
  core = core.split('+').first;
  final dash = core.indexOf('-');
  final pre = dash < 0 ? '' : core.substring(dash + 1);
  if (dash >= 0) core = core.substring(0, dash);
  final parts = core.split('.');
  if (parts.length != 3) return null;
  final nums = <int>[];
  for (final p in parts) {
    if (p.isEmpty || !RegExp(r'^\d+$').hasMatch(p)) return null;
    nums.add(int.parse(p));
  }
  final ids = dash < 0 ? <String>[] : pre.split('.');
  if (ids.any((id) => !_ident.hasMatch(id))) return null;
  return (nums: nums, pre: ids);
}
