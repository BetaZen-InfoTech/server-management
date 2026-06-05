// Message — one email. Two shapes share the same class:
//
//   List shape: from the /mailboxes/:folder/messages endpoint, body
//   fields (bodyHtml, bodyText) are null. Cheap to render in a list.
//
//   Full shape: from the /messages/:id endpoint, body fields are
//   populated. Fetched on demand when the operator opens a message.
//
// One class instead of two so the list-row tap → detail-screen flow
// can pass the partial Message in as a hint and the screen can render
// headers immediately while the full body loads.

class MessageAddress {
  MessageAddress({required this.email, this.name});

  final String email;
  final String? name;

  String get display => name?.trim().isNotEmpty == true ? name! : email;

  factory MessageAddress.fromJson(Map<String, dynamic> json) =>
      MessageAddress(
        email: json['email'] as String,
        name: json['name'] as String?,
      );
}

class Message {
  Message({
    required this.id,
    required this.subject,
    required this.from,
    required this.to,
    this.cc = const [],
    this.bcc = const [],
    required this.date,
    required this.isRead,
    required this.isStarred,
    required this.hasAttachments,
    this.snippet = '',
    this.bodyHtml,
    this.bodyText,
  });

  final String id;
  final String subject;
  final MessageAddress from;
  final List<MessageAddress> to;
  final List<MessageAddress> cc;
  final List<MessageAddress> bcc;
  final DateTime date;
  final bool isRead;
  final bool isStarred;
  final bool hasAttachments;

  /// First ~140 chars of the plain-text body, server-side. Shown in
  /// the list row so the operator can triage without opening.
  final String snippet;

  /// Null on list-shape; populated on the full GET. Renderers should
  /// prefer bodyHtml when present, fall back to bodyText.
  final String? bodyHtml;
  final String? bodyText;

  bool get hasBody => bodyHtml != null || bodyText != null;

  factory Message.fromJson(Map<String, dynamic> json) {
    final fromRaw = json['from'];
    return Message(
      id: json['id'] as String,
      subject: (json['subject'] as String?) ?? '(no subject)',
      from: fromRaw is Map<String, dynamic>
          ? MessageAddress.fromJson(fromRaw)
          : MessageAddress(email: fromRaw?.toString() ?? ''),
      to: _addrList(json['to']),
      cc: _addrList(json['cc']),
      bcc: _addrList(json['bcc']),
      date: DateTime.tryParse(json['date'] as String? ?? '')?.toLocal() ??
          DateTime.now(),
      isRead: json['is_read'] as bool? ?? false,
      isStarred: json['is_starred'] as bool? ?? false,
      hasAttachments: json['has_attachments'] as bool? ?? false,
      snippet: json['snippet'] as String? ?? '',
      bodyHtml: json['body_html'] as String?,
      bodyText: json['body_text'] as String?,
    );
  }

  static List<MessageAddress> _addrList(Object? raw) {
    if (raw is List) {
      return raw
          .whereType<Map<String, dynamic>>()
          .map(MessageAddress.fromJson)
          .toList();
    }
    return const [];
  }
}
