// Mailbox — one IMAP folder (Inbox, Sent, Drafts, Trash, Spam, plus
// arbitrary user-created labels). The backend collapses IMAP's
// hierarchical naming ("Some/Sub/Folder") into a flat list with the
// `path` field carrying the original; we render `name` only.

class Mailbox {
  Mailbox({
    required this.name,
    required this.path,
    required this.unreadCount,
    required this.totalCount,
    this.special,
  });

  /// User-visible folder name.
  final String name;

  /// IMAP path used in requests ("INBOX", "Sent", "Some/Sub/Folder").
  final String path;

  final int unreadCount;
  final int totalCount;

  /// One of: inbox, sent, drafts, trash, spam, archive. Backend uses
  /// IMAP \Special-Use attributes when present and falls back to name
  /// matching otherwise. Null = a plain user folder.
  final String? special;

  bool get isInbox => special == 'inbox';

  factory Mailbox.fromJson(Map<String, dynamic> json) => Mailbox(
        name: json['name'] as String,
        path: json['path'] as String? ?? json['name'] as String,
        unreadCount: (json['unread_count'] as num?)?.toInt() ?? 0,
        totalCount: (json['total_count'] as num?)?.toInt() ?? 0,
        special: json['special'] as String?,
      );
}
