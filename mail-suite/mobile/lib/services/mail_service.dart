// MailService — bridges the existing Mailbox/Message Dart models to
// the live mail-suite backend contract.
//
// Backend endpoints (see backend/internal/routes/api_routes.go):
//   GET    /api/v1/mail/:account_id/folders
//   GET    /api/v1/mail/:account_id/threads?folder=&page=&limit=
//   GET    /api/v1/mail/:account_id/messages/:uid?folder=
//   PATCH  /api/v1/mail/:account_id/messages/:uid?folder=
//   POST   /api/v1/mail/:account_id/send
//
// Messages don't have a stable string ID on the wire — IMAP gives a
// folder-scoped UID. We synthesize `id = "<folder>:<uid>"` so callers
// keep using the existing single-string id and we can decode both
// pieces back when fetching the full body.

import '../models/mailbox.dart';
import '../models/message.dart';
import 'account_service.dart';
import 'api_client.dart';

class MailService {
  MailService({required ApiClient api, required AccountService accounts})
      : _api = api,
        _accounts = accounts;

  final ApiClient _api;
  final AccountService _accounts;

  String _accountId() {
    final a = _accounts.selected;
    if (a == null) {
      throw ApiException(0, 'No mail account selected. Add one in Settings → Accounts.');
    }
    return a.id;
  }

  Future<List<Mailbox>> listMailboxes() async {
    final body = await _api.get('/api/v1/mail/${_accountId()}/folders');
    final raw = body['value'] ?? body['folders'] ?? <Object?>[];
    if (raw is! List) return const [];
    return raw.whereType<Map<String, dynamic>>().map((j) => Mailbox(
          name: (j['name'] as String?) ?? '',
          path: (j['name'] as String?) ?? '',
          unreadCount: (j['unread'] as num?)?.toInt() ?? 0,
          totalCount: (j['total'] as num?)?.toInt() ?? 0,
          special: j['special'] as String?,
        )).toList();
  }

  Future<List<Message>> listMessages(
    String mailboxPath, {
    int page = 1,
    int limit = 50,
  }) async {
    final body = await _api.get(
      '/api/v1/mail/${_accountId()}/threads',
      query: {'folder': mailboxPath, 'page': '$page', 'limit': '$limit'},
    );
    final items = body['items'] ?? body['value'] ?? <Object?>[];
    if (items is! List) return const [];
    return items
        .whereType<Map<String, dynamic>>()
        .map((j) => _headerToMessage(j, mailboxPath))
        .toList();
  }

  Future<Message> getMessage(String id) async {
    final parts = _splitId(id);
    final body = await _api.get(
      '/api/v1/mail/${_accountId()}/messages/${parts.uid}',
      query: {'folder': parts.folder},
    );
    return _bodyToMessage(body, parts.folder, parts.uid);
  }

  Future<void> sendMessage({
    required List<String> to,
    List<String> cc = const [],
    List<String> bcc = const [],
    required String subject,
    required String body,
    bool isHtml = false,
  }) async {
    await _api.post('/api/v1/mail/${_accountId()}/send', body: {
      'to': to.map((a) => {'address': a}).toList(),
      if (cc.isNotEmpty) 'cc': cc.map((a) => {'address': a}).toList(),
      if (bcc.isNotEmpty) 'bcc': bcc.map((a) => {'address': a}).toList(),
      'subject': subject,
      if (isHtml) 'html': body else 'text': body,
    });
  }

  /// Generic flag setter. Supported flags: `seen`, `starred`.
  /// For move/archive/trash use [moveMessage].
  Future<void> setFlag(String id, String flag, bool value) async {
    final parts = _splitId(id);
    Map<String, dynamic> patch;
    switch (flag) {
      case 'seen':
      case 'read':
        patch = {'unread': !value};
        break;
      case 'starred':
      case 'star':
        patch = {'starred': value};
        break;
      default:
        return;
    }
    await _api.put(
      '/api/v1/mail/${_accountId()}/messages/${parts.uid}?folder=${Uri.encodeComponent(parts.folder)}',
      body: patch,
    );
  }

  Future<void> moveMessage(String id, String destFolder) async {
    final parts = _splitId(id);
    await _api.put(
      '/api/v1/mail/${_accountId()}/messages/${parts.uid}?folder=${Uri.encodeComponent(parts.folder)}',
      body: {'folder': destFolder},
    );
  }

  Future<void> deleteMessage(String id) => moveMessage(id, 'Trash');

  // ── helpers ──────────────────────────────────────────────────────

  static Message _headerToMessage(Map<String, dynamic> j, String folder) {
    final fromList = j['from'] as List? ?? const [];
    final fromAddr = fromList.isNotEmpty && fromList.first is Map
        ? MessageAddress(
            email: (fromList.first as Map)['address']?.toString() ?? '',
            name: (fromList.first as Map)['name']?.toString(),
          )
        : MessageAddress(email: '');
    final toList = (j['to'] as List? ?? const [])
        .whereType<Map>()
        .map((m) => MessageAddress(
              email: m['address']?.toString() ?? '',
              name: m['name']?.toString(),
            ))
        .toList();
    final uid = (j['uid'] as num?)?.toInt() ?? 0;
    return Message(
      id: '$folder:$uid',
      subject: (j['subject'] as String?)?.isNotEmpty == true
          ? j['subject'] as String
          : '(no subject)',
      from: fromAddr,
      to: toList,
      date: DateTime.tryParse(j['date'] as String? ?? '')?.toLocal() ?? DateTime.now(),
      isRead: !(j['unread'] as bool? ?? false),
      isStarred: j['starred'] as bool? ?? false,
      hasAttachments: j['has_attach'] as bool? ?? false,
      snippet: (j['snippet'] as String?) ?? '',
    );
  }

  static Message _bodyToMessage(Map<String, dynamic> j, String folder, int uid) {
    final fromList = j['from'] as List? ?? const [];
    final fromAddr = fromList.isNotEmpty && fromList.first is Map
        ? MessageAddress(
            email: (fromList.first as Map)['address']?.toString() ?? '',
            name: (fromList.first as Map)['name']?.toString(),
          )
        : MessageAddress(email: '');
    final toList = (j['to'] as List? ?? const [])
        .whereType<Map>()
        .map((m) => MessageAddress(
              email: m['address']?.toString() ?? '',
              name: m['name']?.toString(),
            ))
        .toList();
    final ccList = (j['cc'] as List? ?? const [])
        .whereType<Map>()
        .map((m) => MessageAddress(
              email: m['address']?.toString() ?? '',
              name: m['name']?.toString(),
            ))
        .toList();
    return Message(
      id: '$folder:$uid',
      subject: (j['subject'] as String?)?.isNotEmpty == true
          ? j['subject'] as String
          : '(no subject)',
      from: fromAddr,
      to: toList,
      cc: ccList,
      date: DateTime.tryParse(j['date'] as String? ?? '')?.toLocal() ?? DateTime.now(),
      isRead: true,
      isStarred: false,
      hasAttachments: (j['attachments'] as List?)?.isNotEmpty ?? false,
      bodyHtml: j['html'] as String?,
      bodyText: j['text'] as String?,
    );
  }

  static _IdParts _splitId(String id) {
    final idx = id.lastIndexOf(':');
    if (idx <= 0) {
      throw ApiException(0, 'Bad message id: $id');
    }
    return _IdParts(
      folder: id.substring(0, idx),
      uid: int.tryParse(id.substring(idx + 1)) ?? 0,
    );
  }
}

class _IdParts {
  _IdParts({required this.folder, required this.uid});
  final String folder;
  final int uid;
}
