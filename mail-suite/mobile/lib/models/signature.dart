// Signature — a per-user HTML signature. The compose screen appends
// the selected signature to outgoing mail server-side.

class Signature {
  Signature({
    required this.id,
    required this.name,
    required this.html,
    required this.isDefault,
  });

  final String id;
  final String name;
  final String html;
  final bool isDefault;

  factory Signature.fromJson(Map<String, dynamic> json) => Signature(
        id: json['id'] as String,
        name: (json['name'] as String?) ?? '',
        html: (json['html'] as String?) ?? '',
        isDefault: json['is_default'] as bool? ?? false,
      );
}
