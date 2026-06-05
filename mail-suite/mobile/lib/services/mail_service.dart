// MailService — the mail-suite backend's mailbox + message API,
// expressed as Dart methods.
//
// Endpoint contract (matches `mail-suite/backend` expectations):
//   GET    /api/v1/mailboxes
//   GET    /api/v1/mailboxes/:path/messages?page=&limit=
//   GET    /api/v1/messages/:id
//   POST   /api/v1/messages                       (send)
//   POST   /api/v1/messages/:id/flag              {flag, value}
//   DELETE /api/v1/messages/:id
//
// The backend hasn't shipped these yet; the path/shape here is what
// the mobile expects. When the backend lands them, this file is the
// canonical reference for the wire shape.

import '../models/mailbox.dart';
import '../models/message.dart';
import 'api_client.dart';

class MailService {
  MailService({required ApiClient api}) : _api = api;
  final ApiClient _api;

  Future<List<Mailbox>> listMailboxes() async {
    final body = await _api.get('/api/v1/mailboxes');
    final raw = body['mailboxes'] ?? body['value'] ?? <Object?>[];
    if (raw is! List) return const [];
    return raw
        .whereType<Map<String, dynamic>>()
        .map(Mailbox.fromJson)
        .toList();
  }

  Future<List<Message>> listMessages(
    String mailboxPath, {
    int page = 1,
    int limit = 50,
  }) async {
    final body = await _api.get(
      '/api/v1/mailboxes/${Uri.encodeComponent(mailboxPath)}/messages',
      query: {'page': '$page', 'limit': '$limit'},
    );
    final raw = body['messages'] ?? body['value'] ?? <Object?>[];
    if (raw is! List) return const [];
    return raw
        .whereType<Map<String, dynamic>>()
        .map(Message.fromJson)
        .toList();
  }

  Future<Message> getMessage(String id) async {
    final body = await _api.get('/api/v1/messages/$id');
    return Message.fromJson(body);
  }

  Future<void> sendMessage({
    required List<String> to,
    List<String> cc = const [],
    List<String> bcc = const [],
    required String subject,
    required String body,
    bool isHtml = false,
  }) async {
    await _api.post('/api/v1/messages', body: {
      'to': to,
      if (cc.isNotEmpty) 'cc': cc,
      if (bcc.isNotEmpty) 'bcc': bcc,
      'subject': subject,
      'body': body,
      'is_html': isHtml,
    });
  }

  Future<void> setFlag(String id, String flag, bool value) async {
    await _api.post('/api/v1/messages/$id/flag', body: {
      'flag': flag,
      'value': value,
    });
  }

  Future<void> deleteMessage(String id) async {
    await _api.delete('/api/v1/messages/$id');
  }
}
