/// `GET /api/shares` 条目（docs/03-file-api.md 4.1）。
class Share {
  const Share({
    required this.id,
    required this.name,
    required this.online,
    required this.perms,
  });

  factory Share.fromJson(Map<String, dynamic> j) => Share(
    id: j['id'] as String,
    name: j['name'] as String? ?? '',
    online: j['status'] != 'offline',
    perms: SharePerms.fromJson(
      (j['perms'] as Map?)?.cast<String, dynamic>() ?? const {},
    ),
  );

  final String id;
  final String name;
  final bool online;
  final SharePerms perms;
}

class SharePerms {
  const SharePerms({
    this.download = false,
    this.upload = false,
    this.modify = false,
    this.showHidden = false,
  });

  factory SharePerms.fromJson(Map<String, dynamic> j) => SharePerms(
    download: j['download'] as bool? ?? false,
    upload: j['upload'] as bool? ?? false,
    modify: j['modify'] as bool? ?? false,
    showHidden: j['showHidden'] as bool? ?? false,
  );

  final bool download;
  final bool upload;
  final bool modify;
  final bool showHidden;
}
