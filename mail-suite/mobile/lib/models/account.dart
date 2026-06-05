// Account — the IMAP mailbox identity the operator is signed in as.
// Stored in shared_preferences on login so the inbox screen can render
// the email + display name in the app bar without re-querying the
// backend. Tokens live in SecureStorage, NOT here.

class Account {
  Account({
    required this.email,
    required this.serverUrl,
    this.displayName,
  });

  /// The mailbox address — also the IMAP login.
  final String email;

  /// The Betazen Mail backend's public URL, including scheme + port.
  /// Stored per-account so a multi-tenant build can talk to several
  /// servers without a rebuild.
  final String serverUrl;

  /// Optional friendly name surfaced in the app bar. Falls back to the
  /// local-part of the email when null.
  final String? displayName;

  String get short =>
      displayName?.trim().isNotEmpty == true
          ? displayName!
          : email.split('@').first;

  Map<String, dynamic> toJson() => {
        'email': email,
        'server_url': serverUrl,
        'display_name': displayName,
      };

  factory Account.fromJson(Map<String, dynamic> json) => Account(
        email: json['email'] as String,
        serverUrl: json['server_url'] as String,
        displayName: json['display_name'] as String?,
      );
}
