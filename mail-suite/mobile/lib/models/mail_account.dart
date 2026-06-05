// MailAccount — a single mailbox attached to the signed-in user.
// Distinct from `Account` (which is the *user* identity + server URL):
// one user can have many MailAccounts (own betazen mailboxes across
// domains, or an external IMAP).

class MailAccount {
  MailAccount({
    required this.id,
    required this.address,
    required this.displayName,
    required this.provider,
    required this.isPrimary,
    this.color,
  });

  final String id;
  final String address;
  final String displayName;
  final String provider;
  final bool isPrimary;
  final String? color;

  String get short =>
      displayName.isNotEmpty ? displayName : address.split('@').first;

  factory MailAccount.fromJson(Map<String, dynamic> json) => MailAccount(
        id: json['id'] as String,
        address: (json['address'] as String?) ?? '',
        displayName: (json['display_name'] as String?) ?? '',
        provider: (json['provider'] as String?) ?? 'betazen',
        isPrimary: json['is_primary'] as bool? ?? false,
        color: json['color'] as String?,
      );
}
